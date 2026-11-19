package api

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/abrardw/lps/internal/auth"
)

func TestMain(m *testing.M) {
	auth.Cost = bcrypt.MinCost // password hashing is tested in internal/auth
	os.Exit(m.Run())
}
