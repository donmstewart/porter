package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"get.porter.sh/porter/pkg/config"
	"get.porter.sh/porter/pkg/storage/filesystem"
	"github.com/cnabio/cnab-go/claim"
	"github.com/cnabio/cnab-go/schema"
	"github.com/cnabio/cnab-go/utils/crud"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_LoadSchema(t *testing.T) {
	t.Run("valid schema", func(t *testing.T) {
		schema := Schema{
			Claims:      "cnab-claim-1.0.0-DRAFT",
			Credentials: "cnab-credentials-1.0.0-DRAFT",
		}

		c := config.NewTestConfig(t)
		storage := crud.NewBackingStore(crud.NewMockStore())
		p := NewManager(c.Config, storage)

		schemaB, err := json.Marshal(schema)
		require.NoError(t, err, "Marshal schema failed")
		err = storage.Save("", "", "schema", schemaB)
		require.NoError(t, err, "Save schema failed")

		err = p.loadSchema()
		require.NoError(t, err, "LoadSchema failed")
		assert.NotEmpty(t, p.schema, "Schema should be populated with the file's data")
	})

	t.Run("missing schema, empty home", func(t *testing.T) {
		c := config.NewTestConfig(t)
		storage := crud.NewBackingStore(crud.NewMockStore())
		p := NewManager(c.Config, storage)

		err := p.loadSchema()
		require.NoError(t, err, "LoadSchema failed")
		assert.NotEmpty(t, p.schema, "Schema should be initialized automatically when PORTER_HOME is empty")
	})

	t.Run("missing schema, existing home data", func(t *testing.T) {
		c := config.NewTestConfig(t)
		storage := crud.NewBackingStore(crud.NewMockStore())
		p := NewManager(c.Config, storage)

		storage.Save("claims", "", "mybun", []byte(""))

		err := p.loadSchema()
		require.NoError(t, err, "LoadSchema failed")
		assert.Empty(t, p.schema, "Schema should be empty because none was loaded")
	})

	t.Run("invalid schema", func(t *testing.T) {
		c := config.NewTestConfig(t)
		storage := crud.NewBackingStore(crud.NewMockStore())
		p := NewManager(c.Config, storage)

		var schemaB = []byte("invalid schema")
		err := storage.Save("", "", "schema", schemaB)
		require.NoError(t, err, "Save schema failed")

		err = p.loadSchema()
		require.Error(t, err, "Expected LoadSchema to fail")
		assert.Contains(t, err.Error(), "could not parse storage schema document")
		assert.Empty(t, p.schema, "Schema should be empty because none was loaded")
	})
}

func TestManager_ShouldMigrateClaims(t *testing.T) {
	testcases := []struct {
		name         string
		claimVersion string
		wantMigrate  bool
	}{
		{"old schema", "cnab-claim-1.0.0-DRAFT", true},
		{"missing schema", "", true},
		{"current schema", claim.CNABSpecVersion, false},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			c := config.NewTestConfig(t)
			storage := crud.NewBackingStore(crud.NewMockStore())
			p := NewManager(c.Config, storage)

			p.schema = Schema{
				Claims: schema.Version(tc.claimVersion),
			}

			assert.Equal(t, tc.wantMigrate, p.ShouldMigrateClaims())
		})
	}
}

func TestManager_MigrateClaims(t *testing.T) {
	const installation = "example-exec-outputs"

	testcases := []struct {
		name        string
		fileName    string
		migrateName bool
	}{
		{name: "Has installation name", fileName: "has-installation", migrateName: false},
		{name: "Has claim name", fileName: "has-name", migrateName: true},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			config := config.NewTestConfig(t)
			home := config.TestContext.UseFilesystem()
			config.SetHomeDir(home)
			defer config.TestContext.Cleanup()

			claimsDir := filepath.Join(home, "claims")
			config.FileSystem.Mkdir(claimsDir, 0755)
			config.TestContext.AddTestFile(filepath.Join("testdata", tc.fileName+".json"), filepath.Join(claimsDir, tc.fileName+".json"))

			dataStore := crud.NewBackingStore(filesystem.NewStore(*config.Config, hclog.NewNullLogger()))
			mgr := NewManager(config.Config, dataStore)
			claimStore := claim.NewClaimStore(crud.NewBackingStore(mgr), nil, nil)

			logfilePath, err := mgr.Migrate()
			require.NoError(t, err, "Migrate failed")

			c, err := claimStore.ReadLastClaim(installation)
			require.NoError(t, err, "could not read claim")
			require.NotNil(t, c, "claim should be populated")
			assert.Equal(t, installation, c.Installation, "claim.Installation was not populated")

			assert.Contains(t, config.TestContext.GetError(), "!!! Migrating claims data", "the claim should have been migrated")
			if tc.migrateName {
				assert.Contains(t, config.TestContext.GetError(), "claim.Name to claim.Installation", "the claim should have been migrated from Name -> Installation")
			} else {
				assert.NotContains(t, config.TestContext.GetError(), "claim.Name to claim.Installation", "the claim should NOT be migrated")
			}

			logfile, err := config.FileSystem.ReadFile(logfilePath)
			require.NoError(t, err, "error reading logfile")
			assert.Equal(t, config.TestContext.GetError(), string(logfile), "the migration should have been copied to both stderr and the logfile")

			// Read a second time, this time there shouldn't be a migration
			config.TestContext.ClearOutputs()
			_, err = claimStore.ReadLastClaim(installation)
			assert.NotContains(t, config.TestContext.GetError(), "!!! Migrating claims data", "the claim should have been migrated a second time")
		})
	}

	t.Run("no migration", func(t *testing.T) {
		config := config.NewTestConfig(t)
		home := config.TestContext.UseFilesystem()
		config.SetHomeDir(home)
		defer config.TestContext.Cleanup()

		config.CopyDirectory(filepath.Join("testdata", "migrated"), home, false)

		dataStore := crud.NewBackingStore(filesystem.NewStore(*config.Config, hclog.NewNullLogger()))
		mgr := NewManager(config.Config, dataStore)
		claimStore := claim.NewClaimStore(crud.NewBackingStore(mgr), nil, nil)

		c, err := claimStore.ReadLastClaim(installation)
		require.NoError(t, err, "could not read claim")
		require.NotNil(t, c, "claim should be populated")
		assert.Equal(t, installation, c.Installation, "claim.Installation was not populated")
		assert.NotContains(t, config.TestContext.GetError(), "!!! Migrating claims data", "the claim should have been migrated")
	})
}

func TestManager_NoMigrationEmptyHome(t *testing.T) {
	config := config.NewTestConfig(t)
	home := config.TestContext.UseFilesystem()
	config.SetHomeDir(home)
	defer config.TestContext.Cleanup()

	dataStore := crud.NewBackingStore(filesystem.NewStore(*config.Config, hclog.NewNullLogger()))
	mgr := NewManager(config.Config, dataStore)
	claimStore := claim.NewClaimStore(crud.NewBackingStore(mgr), nil, nil)

	_, err := claimStore.ListInstallations()
	require.NoError(t, err, "ListInstallations failed")
}

func TestManager_MigrateInstall(t *testing.T) {
	config := config.NewTestConfig(t)
	home := config.TestContext.UseFilesystem()
	config.SetHomeDir(home)
	defer config.TestContext.Cleanup()

	dataStore := crud.NewBackingStore(filesystem.NewStore(*config.Config, hclog.NewNullLogger()))
	mgr := NewManager(config.Config, dataStore)
	claimStore := claim.NewClaimStore(mgr, nil, nil)

	claimsDir := filepath.Join(home, "claims")
	config.FileSystem.Mkdir(claimsDir, 0755)
	config.TestContext.AddTestFile("testdata/installed.json", filepath.Join(claimsDir, "installed.json"))

	_, err := mgr.Migrate()
	require.NoError(t, err, "Migrate failed")

	exists, _ := config.FileSystem.Exists(filepath.Join(claimsDir, "installed.json"))
	assert.False(t, exists, "the original claim should be removed")

	i, err := claimStore.ReadInstallation("mybun")
	require.NoError(t, err, "ReadInstallation of the migrated claim failed")
	assert.Equal(t, "mybun", i.Name)
	assert.Len(t, i.Claims, 1, "expected 1 claim")

	c, err := i.GetLastClaim()
	require.NoError(t, err)
	assert.Equal(t, claim.ActionInstall, c.Action)
	assert.Equal(t, claim.StatusSucceeded, i.GetLastStatus())
}

func TestManager_MigrateUpgrade(t *testing.T) {
	config := config.NewTestConfig(t)
	home := config.TestContext.UseFilesystem()
	config.SetHomeDir(home)
	defer config.TestContext.Cleanup()

	dataStore := crud.NewBackingStore(filesystem.NewStore(*config.Config, hclog.NewNullLogger()))
	mgr := NewManager(config.Config, dataStore)
	claimStore := claim.NewClaimStore(mgr, nil, nil)

	claimsDir := filepath.Join(home, "claims")
	config.FileSystem.Mkdir(claimsDir, 0755)
	config.TestContext.AddTestFile("testdata/upgraded.json", filepath.Join(claimsDir, "upgraded.json"))

	_, err := mgr.Migrate()
	require.NoError(t, err, "Migrate failed")

	exists, _ := config.FileSystem.Exists(filepath.Join(claimsDir, "upgraded.json"))
	assert.False(t, exists, "the original claim should be removed")

	i, err := claimStore.ReadInstallation("mybun")
	require.NoError(t, err, "ReadInstallation of the migrated claim failed")
	assert.Equal(t, "mybun", i.Name)
	assert.Len(t, i.Claims, 2, "expected 2 claims")

	c, err := i.GetLastClaim()
	require.NoError(t, err)
	assert.Equal(t, claim.ActionUpgrade, c.Action)
	assert.Equal(t, claim.StatusSucceeded, i.GetLastStatus())

	installClaim := i.Claims[0]
	assert.Equal(t, claim.ActionInstall, installClaim.Action)
	assert.Equal(t, claim.StatusUnknown, installClaim.GetStatus())
}
