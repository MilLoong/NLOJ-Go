package user

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"regexp"
)

type Account struct {
	ID      int      `json:"id"`
	Name    string   `json:"username"`
	Role    string   `json:"role"`
	Created string   `json:"createTime"`
	Salt    [16]byte `json:"-"`
	Hash    []byte   `json:"-"`
}

var usernamePattern = regexp.MustCompile("^[A-Za-z0-9_]{3,32}$")

func ValidCredentials(name, password string) bool {
	return usernamePattern.MatchString(name) && len(password) >= 6 && len(password) <= 64
}

func PasswordHash(password string, salt [16]byte) []byte {
	hash, err := pbkdf2.Key(sha256.New, password, salt[:], 600000, 32)
	if err != nil {
		panic(err)
	}
	return hash
}
