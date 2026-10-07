// SPDX-License-Identifier: Unlicense OR MIT

package sandbox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/tetratelabs/wazero"
	"golang.org/x/sys/cpu"
)

// A compiled module is machine code, and wazero runs what its compilation
// cache gives back without checking it again: an entry on disk that someone
// replaced is code run in this process. So the disk cache keeps entries
// whose authenticity it can prove. Each is signed with HMAC-SHA256 under a
// key kept apart from the cache, beside the user's settings, and the
// signature covers the entry's key, the wazero version and this machine's
// CPU features. An entry that fails is deleted and compiled again. Get reads
// an entry whole and hands wazero the bytes it checked, so nothing can change
// them between the check and their use.
//
// This holds against what can write files but not read the key: another
// user, a file written where it should not be, a damaged disk. It does not
// hold against a program running as the same user, which can as well read
// the key or change the client itself.

// maxEntry bounds an entry read back from disk.
const maxEntry = 256 << 20

// entryMagic starts every entry on disk.
var entryMagic = []byte("KGWASM1\n")

// Cache is compiled code shared by the runtimes that use it: in memory
// always, and on disk when it was made by NewDiskCache.
type Cache struct {
	wazero wazero.CompilationCache
	disk   *diskCache
}

// NewMemoryCache returns a cache kept only in memory, for this process.
func NewMemoryCache() *Cache {
	return &Cache{wazero: wazero.NewCompilationCache()}
}

// NewDiskCache returns a cache that also keeps compiled code in dir, signed
// with the key in keyFile, which it creates when it is missing. dir and
// keyFile should be in different directories: the key must not be where the
// entries are. It fails when either is not this user's alone, or when this
// version of wazero cannot take the cache; the caller then keeps a memory
// cache.
func NewDiskCache(dir, keyFile string) (*Cache, error) {
	key, err := loadKey(keyFile)
	if err != nil {
		return nil, fmt.Errorf("sandbox: cache key: %w", err)
	}
	fingerprint := machineFingerprint()
	sub := filepath.Join(dir, fmt.Sprintf("%s-%s-%s", runtime.GOOS, runtime.GOARCH, hex.EncodeToString(fingerprint[:6])))
	for _, d := range []string{dir, sub} {
		if err := privateDir(d); err != nil {
			return nil, fmt.Errorf("sandbox: cache directory: %w", err)
		}
	}
	disk := &diskCache{dir: sub, key: key, context: entryContext(fingerprint)}
	c := wazero.NewCompilationCache()
	if err := setFileCache(c, disk); err != nil {
		return nil, err
	}
	return &Cache{wazero: c, disk: disk}, nil
}

// Persistent reports whether the cache keeps compiled code on disk.
func (c *Cache) Persistent() bool { return c.disk != nil }

// Close frees the compiled code the cache holds in memory.
func (c *Cache) Close(ctx context.Context) error { return c.wazero.Close(ctx) }

var shared struct {
	sync.Mutex
	cache *Cache
}

// SetCache sets the cache that runtimes created after it share. Until it is
// called they share a memory cache. Runtimes created before keep theirs.
func SetCache(c *Cache) {
	shared.Lock()
	defer shared.Unlock()
	shared.cache = c
}

func currentCache() *Cache {
	shared.Lock()
	defer shared.Unlock()
	if shared.cache == nil {
		shared.cache = NewMemoryCache()
	}
	return shared.cache
}

// errHook is returned when wazero's compilation cache no longer has the
// field the disk cache is set in.
var errHook = errors.New("sandbox: this wazero version does not take a disk cache")

// setFileCache gives wazero's cache c the disk store fc. wazero accepts
// only a directory, read and written as is, and keeps its interface for a
// store internal; the cache it returns holds that store in a field fileCache,
// unset for a memory cache. fc has the interface's methods, so it is put
// there. When the field is not found, or fc does not fit it, nothing changes.
func setFileCache(c wazero.CompilationCache, fc *diskCache) error {
	v := reflect.ValueOf(c)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return errHook
	}
	f := v.Elem().FieldByName("fileCache")
	if !f.IsValid() || f.Kind() != reflect.Interface || !f.IsNil() || !reflect.TypeOf(fc).Implements(f.Type()) {
		return errHook
	}
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(fc))
	return nil
}

// diskCache keeps signed entries in dir. Its methods are those of wazero's
// internal filecache.Cache, and may be called from many goroutines.
type diskCache struct {
	dir     string
	key     []byte
	context []byte

	hits, misses, rejected, added atomic.Int64
}

func (d *diskCache) path(key [sha256.Size]byte) string {
	return filepath.Join(d.dir, hex.EncodeToString(key[:]))
}

// sign returns the signature of an entry under key.
func (d *diskCache) sign(key [sha256.Size]byte, content []byte) []byte {
	m := hmac.New(sha256.New, d.key)
	m.Write(d.context)
	m.Write(key[:])
	m.Write(content)
	return m.Sum(nil)
}

// Get returns the entry under key when its signature holds; a missing,
// unreadable or forged entry is a miss, and a forged one is deleted.
func (d *diskCache) Get(key [sha256.Size]byte) (io.ReadCloser, bool, error) {
	data, err := readEntry(d.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		d.misses.Add(1)
		return nil, false, nil
	}
	if err == nil {
		head := len(entryMagic) + sha256.Size
		if len(data) > head && bytes.Equal(data[:len(entryMagic)], entryMagic) {
			content := data[head:]
			if hmac.Equal(data[len(entryMagic):head], d.sign(key, content)) {
				d.hits.Add(1)
				return io.NopCloser(bytes.NewReader(content)), true, nil
			}
		}
	}
	d.rejected.Add(1)
	_ = os.Remove(d.path(key))
	return nil, false, nil
}

// Add signs content and stores it under key, replacing what was there at
// once: it is written aside and renamed.
func (d *diskCache) Add(key [sha256.Size]byte, content io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(content, maxEntry+1))
	if err != nil {
		return err
	}
	if len(data) > maxEntry {
		return fmt.Errorf("sandbox: compiled module over %d bytes", maxEntry)
	}
	f, err := os.CreateTemp(d.dir, ".tmp-*")
	if err != nil {
		return err
	}
	err = func() error {
		for _, b := range [][]byte{entryMagic, d.sign(key, data), data} {
			if _, err := f.Write(b); err != nil {
				return err
			}
		}
		if err := f.Sync(); err != nil {
			return err
		}
		return f.Close()
	}()
	if err == nil {
		err = os.Rename(f.Name(), d.path(key))
	}
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return err
	}
	d.added.Add(1)
	return nil
}

// Delete removes the entry under key, which wazero asks for when an entry
// was made by another version of it.
func (d *diskCache) Delete(key [sha256.Size]byte) error {
	err := os.Remove(d.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// readEntry reads a whole entry, which must be this user's regular file, not
// a link, and not larger than maxEntry.
func readEntry(path string) ([]byte, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxEntry+int64(len(entryMagic)+sha256.Size) {
		return nil, errors.New("not a cache entry")
	}
	if err := ownedPrivately(st, false); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(f, st.Size()+1))
}

// loadKey reads the 32-byte key in path, or makes a new one there. A key
// that is not this user's alone is replaced, which drops the entries signed
// with it: anyone could have read it.
func loadKey(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := openNoFollow(path)
	if err == nil {
		key, err := func() ([]byte, error) {
			defer f.Close()
			st, err := f.Stat()
			if err != nil {
				return nil, err
			}
			if !st.Mode().IsRegular() || st.Size() != sha256.Size {
				return nil, errors.New("not a key")
			}
			if err := ownedPrivately(st, true); err != nil {
				return nil, err
			}
			key := make([]byte, sha256.Size)
			_, err = io.ReadFull(f, key)
			return key, err
		}()
		if err == nil {
			return key, nil
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("%s cannot be replaced: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, sha256.Size)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	// O_EXCL: when two processes make the key at once, one fails and keeps
	// a memory cache this time, rather than signing with a key not stored.
	f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return key, f.Close()
}

// machineFingerprint identifies the CPU features compiled code may rely on,
// so that a cache in a home directory shared between machines does not hand
// one machine code compiled for another.
func machineFingerprint() [sha256.Size]byte {
	return sha256.Sum256(fmt.Appendf(nil, "%s/%s %+v %+v", runtime.GOOS, runtime.GOARCH, cpu.X86, cpu.ARM64))
}

// entryContext is what every signature covers besides an entry: the format,
// the wazero version and the machine.
func entryContext(fingerprint [sha256.Size]byte) []byte {
	version := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, m := range info.Deps {
			if m.Path == "github.com/tetratelabs/wazero" {
				version = m.Version
				if m.Replace != nil {
					version = m.Replace.Path + "@" + m.Replace.Version
				}
			}
		}
	}
	return fmt.Appendf(nil, "komarugram wasm cache 1\x00%s\x00%x\x00", version, fingerprint)
}
