package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// makeThumbnail builds a deterministic thumbnail blob from object bytes.
// No image codec dependency — proof is one stable object per attachment_id.
func makeThumbnail(objectKey string, content []byte) (thumbKey string, thumb []byte) {
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	prefix := content
	if len(prefix) > 64 {
		prefix = prefix[:64]
	}
	thumb = []byte(fmt.Sprintf("THUMB\nobject=%s\nsha256=%s\n", objectKey, digest))
	thumb = append(thumb, prefix...)
	thumbKey = thumbnailObjectKey(objectKey)
	return thumbKey, thumb
}

func thumbnailObjectKey(objectKey string) string {
	// notes/{note}/{att}/file.jpg → notes/{note}/{att}/thumb.bin
	if i := lastSlash(objectKey); i >= 0 {
		return objectKey[:i+1] + "thumb.bin"
	}
	return objectKey + ".thumb.bin"
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}
