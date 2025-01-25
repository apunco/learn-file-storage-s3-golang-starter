package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't get video by id", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "", err)
		return
	}

	fmt.Println("uploading video by user", userID)

	maxMemory := int64(1 << 30)

	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse form", err)
		return
	}

	file, fileHeader, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't read thumbnail header", err)
		return
	}

	fileExtension := strings.Split(fileHeader.Header.Get("Content-Type"), "/")[1]
	fileReader := io.Reader(file)
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(fileHeader.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Error parsing mime type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Incorrect media type", err)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Error saving video to memory", err)
		return
	}

	writer := io.Writer(tempFile)
	_, err = io.Copy(writer, fileReader)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't copy video into a temp file", err)
		return
	}

	tempFile.Seek(0, io.SeekStart)
	ratio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get aspect ration", err)
		return
	}

	tempFile.Seek(0, io.SeekStart)
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	b := make([]byte, 32)

	_, err = rand.Read(b)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating random bits", err)
		return
	}

	fileKey := fmt.Sprintf("%x.%s", b, fileExtension)

	switch ratio {
	case "16:9":
		fileKey = fmt.Sprintf("landscape/%s", fileKey)
	case "9:16":
		fileKey = fmt.Sprintf("portrait/%s", fileKey)
	default:
		fileKey = fmt.Sprintf("other/%s", fileKey)
	}

	objectInput := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileKey,
		Body:        tempFile,
		ContentType: &mediaType,
	}
	fmt.Printf("starting file upload")

	s3Output, err := cfg.s3Client.PutObject(r.Context(), &objectInput)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed uploading to s3", err)
		return
	}

	videoUrl := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileKey)
	video.VideoURL = &videoUrl

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't update video with the new thumbnail", err)
		return
	}

	respondWithJSON(w, http.StatusOK, s3Output)

}

func getVideoAspectRatio(filePath string) (string, error) {

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("file does not exist at path: %s", filePath)
	}

	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("ffprobe error: %v, stderr: %s", err, stderr.String())
	}

	type Stream struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}

	type FFProbeOutput struct {
		Streams []Stream `json:"streams"`
	}

	vd := FFProbeOutput{}
	err = json.Unmarshal(stdout.Bytes(), &vd)
	if err != nil {
		return "", err
	}

	width := vd.Streams[0].Width
	height := vd.Streams[0].Height

	ratio := float64(width) / float64(height)
	if ratio > 1.7 && ratio < 1.8 {
		return "16:9", nil
	} else if ratio > 0.5 && ratio < 0.6 {
		return "9:16", nil
	} else {
		return "other", nil
	}
}
