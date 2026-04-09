package ocpp_test

import (
	"fmt"
	"testing"

	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalAuthList_FullUpdate(t *testing.T) {
	m := ocpp.NewLocalAuthListManager()
	entries := []ocpp.LocalAuthEntry{
		{IDTag: "ABC", Status: "Accepted"},
		{IDTag: "DEF", Status: "Blocked"},
	}
	require.NoError(t, m.UpdateList(1, entries, "Full"))

	version, count, _, _ := m.GetStats()
	assert.Equal(t, 1, version)
	assert.Equal(t, 2, count)
}

func TestLocalAuthList_DifferentialUpdate(t *testing.T) {
	m := ocpp.NewLocalAuthListManager()
	_ = m.UpdateList(1, []ocpp.LocalAuthEntry{{IDTag: "ABC", Status: "Accepted"}}, "Full")
	_ = m.UpdateList(2, []ocpp.LocalAuthEntry{{IDTag: "XYZ", Status: "Blocked"}}, "Differential")

	version, count, _, _ := m.GetStats()
	assert.Equal(t, 2, version)
	assert.Equal(t, 2, count) // ABC still there, XYZ added

	entry := m.GetEntry("ABC")
	assert.NotNil(t, entry)
}

func TestLocalAuthList_Remove(t *testing.T) {
	m := ocpp.NewLocalAuthListManager()
	_ = m.UpdateList(1, []ocpp.LocalAuthEntry{{IDTag: "ABC", Status: "Accepted"}}, "Full")
	require.NoError(t, m.RemoveEntry("ABC"))
	assert.Nil(t, m.GetEntry("ABC"))
}

func TestLocalAuthList_MaxEntries(t *testing.T) {
	m := ocpp.NewLocalAuthListManager()
	entries := make([]ocpp.LocalAuthEntry, 1001)
	for i := range entries {
		entries[i] = ocpp.LocalAuthEntry{IDTag: fmt.Sprintf("tag%d", i), Status: "Accepted"}
	}
	err := m.UpdateList(1, entries, "Full")
	assert.Error(t, err) // exceeds 1000 max entries
}
