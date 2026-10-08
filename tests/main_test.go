package tests

import (
	"log"
	"os"
	"testing"

	"grpc-auth/tests/suite"
)

func TestMain(m *testing.M) {
	if err := suite.Start(); err != nil {
		log.Fatalf("failed to start test server: %v", err)
	}

	code := m.Run()

	suite.Stop()
	os.Exit(code)
}
