package auth

import (
	"errors"
	"net/http"
	"strings"
)

func GetBearerToken(headers http.Header) (string, error) {
	authorization := headers.Get("Authorization")
	if authorization == "" {
		return "", errors.New("no authorization header found")
	}

	parts := strings.Fields(authorization)
	if len(parts) < 2 {
		return "", errors.New("malformed authorization header")
	}
	if parts[0] != "Bearer" {
		return "", errors.New("malformed authorization header. bearer missing")
	}

	return parts[1], nil
}
