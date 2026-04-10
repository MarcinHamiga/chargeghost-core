package ocpp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalAuthListManager_SaveLoadState(t *testing.T) {
	dir := t.TempDir()

	m := NewLocalAuthListManager()
	expiry := time.Now().Add(1 * time.Hour)
	parent := "PARENT"
	require.NoError(t, m.UpdateList(5, []LocalAuthEntry{
		{IDTag: "TAG1", Status: "Accepted", Expiry: &expiry, ParentIDTag: &parent},
		{IDTag: "TAG2", Status: "Blocked"},
	}, "Full"))

	require.NoError(t, m.SaveState(dir))

	m2 := NewLocalAuthListManager()
	require.NoError(t, m2.LoadState(dir))

	assert.Equal(t, 5, m2.GetVersion())
	e := m2.GetEntry("TAG1")
	require.NotNil(t, e)
	assert.Equal(t, "Accepted", e.Status)
	assert.Equal(t, "PARENT", *e.ParentIDTag)

	e2 := m2.GetEntry("TAG2")
	require.NotNil(t, e2)
	assert.Equal(t, "Blocked", e2.Status)
}

func TestLocalAuthListManager_LoadState_MissingFile(t *testing.T) {
	m := NewLocalAuthListManager()
	err := m.LoadState(t.TempDir())
	assert.NoError(t, err)
	assert.Equal(t, 0, m.GetVersion())
}

func TestLocalAuthListManager_SaveLoadState_AfterDifferentialDelete(t *testing.T) {
	dir := t.TempDir()

	m := NewLocalAuthListManager()
	require.NoError(t, m.UpdateList(1, []LocalAuthEntry{
		{IDTag: "TAG1", Status: "Accepted"},
		{IDTag: "TAG2", Status: "Blocked"},
	}, "Full"))
	require.NoError(t, m.UpdateList(2, []LocalAuthEntry{{IDTag: "TAG1", Delete: true}}, "Differential"))
	require.NoError(t, m.SaveState(dir))

	m2 := NewLocalAuthListManager()
	require.NoError(t, m2.LoadState(dir))

	assert.Equal(t, 2, m2.GetVersion())
	assert.Nil(t, m2.GetEntry("TAG1"))
	e := m2.GetEntry("TAG2")
	require.NotNil(t, e)
	assert.Equal(t, "Blocked", e.Status)
}

func TestAuthorizationCache_SaveLoadState(t *testing.T) {
	dir := t.TempDir()

	c := NewAuthorizationCache()
	expiry := time.Now().Add(1 * time.Hour)
	c.Put("TAG1", "Accepted", &expiry)
	c.Put("TAG2", "Blocked", nil)

	require.NoError(t, c.SaveState(dir))

	c2 := NewAuthorizationCache()
	require.NoError(t, c2.LoadState(dir))

	status, exp, found := c2.Get("TAG1")
	assert.True(t, found)
	assert.Equal(t, "Accepted", status)
	assert.NotNil(t, exp)

	status2, _, found2 := c2.Get("TAG2")
	assert.True(t, found2)
	assert.Equal(t, "Blocked", status2)
}

func TestAuthorizationCache_LoadState_MissingFile(t *testing.T) {
	c := NewAuthorizationCache()
	err := c.LoadState(t.TempDir())
	assert.NoError(t, err)
	assert.Equal(t, 0, c.Size())
}
