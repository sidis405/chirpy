package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const tokenDuration = time.Duration(3600) * time.Second

func MakeJWT(userID uuid.UUID, tokenSecret string) (string, error) {
	signingKey := []byte(tokenSecret)
	claims := &jwt.RegisteredClaims{
		Issuer:    "chirpy",
		Subject:   userID.String(),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenDuration)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	jwt, err := token.SignedString(signingKey)
	return jwt, err
}

func ValidateJWT(tokenString, tokenSecret string) (uuid.UUID, error) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return []byte(tokenSecret), nil
	})

	var userUuid uuid.UUID

	if err != nil {
		return userUuid, err
	}
	subject, err := token.Claims.GetSubject()
	if err != nil {
		return userUuid, err
	}
	userUuid, err = uuid.Parse(subject)
	if err != nil {
		return userUuid, err
	}

	return userUuid, nil
}
