package invenio_test

import (
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/contracttest"
	"github.com/BU-Neuromics/datapin/internal/backend/invenio"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakeinvenio"
)

// The invenio driver against fakeinvenio must satisfy the cross-adapter
// contract — the same suite every other adapter runs.
func TestContract_Invenio(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) backend.Backend {
		srv := fakeinvenio.New(testToken)
		t.Cleanup(srv.Close)
		c, err := invenio.New(srv.URL(), testToken)
		if err != nil {
			t.Fatal(err)
		}
		return c
	})
}
