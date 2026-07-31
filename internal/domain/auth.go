package domain

import "errors"

// ErrGameNotFound означает, что игра с указанным productId отсутствует.
var ErrGameNotFound = errors.New("game not found")

// GoogleIdentity содержит проверенные данные Google OpenID Connect.
type GoogleIdentity struct {
	Subject   string
	Email     string
	Name      string
	AvatarURL string
}

// User — локальная учётная запись, связанная со стабильным Google subject.
type User struct {
	ID        int64
	Email     string
	Name      string
	AvatarURL string
}
