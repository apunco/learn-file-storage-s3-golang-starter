package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	maxMemory := int64(10 << 20)

	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse form", err)
		return
	}

	file, _, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't read thumbnail header", err)
		return
	}

	mediaType := r.Header.Get("Content-Type")

	fileReader := io.Reader(file)
	imageData, err := io.ReadAll(fileReader)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't read thumbnail data", err)
		return
	}

	rawImageData := base64.StdEncoding.EncodeToString(imageData)
	dataUrl := fmt.Sprintf("data:%s;base64,%s", mediaType, rawImageData)

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't get video by id", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "", err)
		return
	}

	video.ThumbnailURL = &dataUrl

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't update video with the new thumbnail", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
