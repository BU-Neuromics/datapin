package dataverse_test

import (
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/contracttest"
	"github.com/BU-Neuromics/datapin/internal/backend/dataverse"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakedataverse"
)

const testToken = "dv-token"

func TestContract_Dataverse(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) backend.Backend {
		srv := fakedataverse.New(testToken)
		t.Cleanup(srv.Close)
		c, err := dataverse.New(srv.URL(), testToken)
		if err != nil {
			t.Fatal(err)
		}
		return c
	})
}

// Keys with directory components map onto directoryLabel and round-trip.
func TestDataverse_PathKeys(t *testing.T) {
	srv := fakedataverse.New(testToken)
	t.Cleanup(srv.Close)
	c, err := dataverse.New(srv.URL(), testToken)
	if err != nil {
		t.Fatal(err)
	}
	contracttest.RunPathKeyRoundTrip(t, c)
}
