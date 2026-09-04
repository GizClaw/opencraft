package migrations

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// discoverMigrations loads every *.sql file under dir from the
// embedded filesystem and returns one db.Migration per file, ordered
// by filename. Migration files are named
// <version>_<description>.sql; the numeric prefix is the schema
// migration version and is preserved for v0.1.0 compatibility.
func discoverMigrations(sqlFS fs.FS, dir string) []db.Migration {
	pattern := dir + "/*.sql"
	names, err := fs.Glob(sqlFS, pattern)
	if err != nil {
		panic(fmt.Sprintf("migrations: glob %s: %v", pattern, err))
	}

	type entry struct {
		name    string
		version int
		sql     string
	}
	entries := make([]entry, 0, len(names))
	seen := make(map[int]string, len(names))
	for _, name := range names {
		sql, err := fs.ReadFile(sqlFS, name)
		if err != nil {
			panic(fmt.Sprintf("migrations: read %s: %v", name, err))
		}
		base := path.Base(name)
		prefix, _, _ := strings.Cut(base, "_")
		version, err := strconv.Atoi(prefix)
		if err != nil || version <= 0 {
			panic(fmt.Sprintf("migrations: %s has invalid version prefix", name))
		}
		if prev, ok := seen[version]; ok {
			panic(fmt.Sprintf(
				"migrations: version %d appears in both %s and %s",
				version, prev, name))
		}
		seen[version] = name
		entries = append(entries, entry{
			name:    strings.TrimSuffix(base, path.Ext(base)),
			version: version,
			sql:     string(sql),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].version < entries[j].version
	})
	migrations := make([]db.Migration, 0, len(entries))
	for _, e := range entries {
		migrations = append(migrations, db.Migration{
			Version: e.version,
			Name:    e.name,
			Statements: []string{
				e.sql,
			},
		})
	}
	return migrations
}
