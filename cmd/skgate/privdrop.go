package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/helv-io/skgate/internal/config"
)

// Default unprivileged identity the server runs as (override with PUID / PGID).
const (
	defaultPUID = 1000
	defaultPGID = 1000
)

// targetIDs computes the uid:gid the server must run as from PUID / PGID.
// Empty values fall back to 1000. Root (0) is rejected: skgate never serves as root.
func targetIDs(getenv func(string) string) (uid, gid int, err error) {
	uid, err = parseID("PUID", getenv("PUID"), defaultPUID)
	if err != nil {
		return 0, 0, err
	}
	gid, err = parseID("PGID", getenv("PGID"), defaultPGID)
	if err != nil {
		return 0, 0, err
	}
	return uid, gid, nil
}

func parseID(name, val string, def int) (int, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return def, nil
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 0 || n > 1<<31-2 {
		return 0, fmt.Errorf("%s=%q is not a valid id", name, val)
	}
	if n == 0 {
		return 0, fmt.Errorf("%s=0 refused: skgate never runs as root", name)
	}
	return n, nil
}

// dataDir is the directory that must be writable by the server (parent of DB_PATH).
func dataDir(dbPath string) string { return filepath.Dir(dbPath) }

// needsDrop reports whether startup must chown and drop privileges (only when running as root).
func needsDrop(euid int) bool { return euid == 0 }

// needsChown reports whether an entry owned by curUID:curGID must be re-owned to uid:gid.
func needsChown(curUID, curGID, uid, gid int) bool { return curUID != uid || curGID != gid }

// chownTree recursively chowns root to uid:gid, touching only entries whose ownership differs.
// Symlinks are not followed (lchown). It is best effort: errors are logged and counted, never fatal.
func chownTree(root string, uid, gid int) (changed, failed int) {
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Printf("privdrop: chown skip %s: %v", p, err)
			failed++
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			log.Printf("privdrop: stat %s: %v", p, err)
			failed++
			return nil
		}
		cu, cg, ok := fileOwner(fi)
		if ok && !needsChown(cu, cg, uid, gid) {
			return nil
		}
		if err := os.Lchown(p, uid, gid); err != nil {
			log.Printf("privdrop: chown %s to %d:%d failed: %v", p, uid, gid, err)
			failed++
			return nil
		}
		changed++
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		log.Printf("privdrop: walk %s: %v", root, walkErr)
		failed++
	}
	return changed, failed
}

// prepareAndDrop is called at startup. If the process is not root (for example compose sets
// `user:`), it does nothing. If root, it creates and chowns the data dir to PUID:PGID and then
// permanently drops to that identity. It returns an error if the drop cannot be completed and
// verified; the caller must then refuse to serve.
func prepareAndDrop(dbPath string) error {
	if !needsDrop(os.Geteuid()) {
		log.Printf("privdrop: running as uid=%d gid=%d, no chown/drop needed", os.Geteuid(), os.Getegid())
		return nil
	}
	uid, gid, err := targetIDs(os.Getenv)
	if err != nil {
		return err
	}
	dir := dataDir(dbPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		log.Printf("privdrop: mkdir %s: %v (continuing)", dir, err)
	}
	changed, failed := chownTree(dir, uid, gid)
	switch {
	case failed > 0:
		log.Printf("privdrop: WARNING data dir %s: %d entries re-owned to %d:%d, %d failed (server may not be able to write)", dir, changed, uid, gid, failed)
	case changed > 0:
		log.Printf("privdrop: data dir %s: re-owned %d entries to %d:%d", dir, changed, uid, gid)
	default:
		log.Printf("privdrop: data dir %s already owned by %d:%d", dir, uid, gid)
	}
	if err := dropPrivileges(uid, gid, config.ManagedAvailable()); err != nil {
		return err
	}
	log.Printf("privdrop: dropped root, now running as uid=%d gid=%d", os.Getuid(), os.Getgid())
	return nil
}

// privateDB takes group and other access off the database files, which older versions created as
// 0644. The key file next to them is 0600 already.
func privateDB(dbPath string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Chmod(dbPath+suffix, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("privdrop: chmod %s: %v", dbPath+suffix, err)
		}
	}
}
