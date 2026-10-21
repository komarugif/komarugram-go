// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"komarugram/pkg/sealedfile"
)

// Media over smallMedia bytes are kept as files sealed in chunks
// (docs/MEDIA_STORE.md): SQLite reads small BLOBs faster than the file
// system, but a large one went through its journal and its single
// connection, with every other reader of the cache waiting.
const (
	smallMedia = 64 << 10
	// MediaChunk is the size of a chunk of a media file: a part of a
	// Telegram download.
	MediaChunk = sealedfile.ChunkSize
	// filesBudget bounds the media files; with mediaBudget, the small
	// media in the database, an account's cache takes 512 MiB.
	filesBudget = 448 << 20
	// flushEvery chunks written, the index of the files is saved.
	flushEvery = 32
	// segmentChunks chunks make a segment, a file of its own: a medium
	// read in part takes no more than its segments read, on a file system
	// that keeps no sparse files (NTFS but for files marked so, BFS) too.
	segmentChunks = 64
)

var (
	// ErrMissing is a chunk of a media file not written yet.
	ErrMissing = errors.New("historycache: chunk not cached")
	// errClosed is the media files of a closed cache, or of one whose
	// protection changed.
	errClosed = errors.New("historycache: media files closed")
)

// mediaFiles keeps the large media of an account in dir, their index in
// the table media_files of the cache.
type mediaFiles struct {
	c       *Cache
	dir     string
	key     []byte
	alg     sealedfile.Algorithm
	nameKey []byte
	budget  int64

	// mu guards what follows; no file is read or written with it held,
	// and the cache's mutex is never taken with it held.
	mu      sync.Mutex
	closed  bool
	entries map[string]*mediaEntry
	// bytes is what the chunks written take; written counts the chunks
	// written since the index was saved.
	bytes   int64
	written int
}

// mediaEntry is a medium kept in files, a segment each: which of its chunks
// are there, and who uses it now.
type mediaEntry struct {
	id, name string
	size     int64
	chunks   []byte
	present  int64
	used     int64
	refs     int
	// files are the segments open, by number, while the entry is used.
	files map[int]*sealedfile.File
	dirty bool
}

func (e *mediaEntry) segments() int { return (e.count() + segmentChunks - 1) / segmentChunks }

// segmentSize is the size of what segment k holds.
func (e *mediaEntry) segmentSize(k int) int64 {
	return min(segmentChunks*MediaChunk, e.size-int64(k)*segmentChunks*MediaChunk)
}

// closeFiles closes the segments open.
func (e *mediaEntry) closeFiles() {
	for k, file := range e.files {
		file.Close()
		delete(e.files, k)
	}
}

func (e *mediaEntry) count() int { return int((e.size + MediaChunk - 1) / MediaChunk) }
func (e *mediaEntry) has(i int) bool {
	return i >= 0 && i < e.count() && e.chunks[i/8]&(1<<(i%8)) != 0
}
func (e *mediaEntry) complete() bool { return e.present == e.size }
func (e *mediaEntry) chunkLen(i int) int64 {
	return min(MediaChunk, e.size-int64(i)*MediaChunk)
}

// SetMediaDir keeps the large media in dir, rather than beside the database.
// It is called before the media are used.
func (c *Cache) SetMediaDir(dir string) {
	c.filesMu.Lock()
	defer c.filesMu.Unlock()
	c.mediaDir = dir
}

// files is the cache's media files, opened at their first use.
func (c *Cache) files() (*mediaFiles, error) {
	c.filesMu.Lock()
	defer c.filesMu.Unlock()
	if c.mediaFiles != nil {
		return c.mediaFiles, nil
	}
	dir := c.mediaDir
	if dir == "" {
		dir = c.path + ".media"
	}
	f := &mediaFiles{c: c, dir: dir, budget: filesBudget, entries: map[string]*mediaEntry{}}
	c.mu.Lock()
	closed, encrypted := c.closed, c.encrypted
	c.mu.Unlock()
	if closed {
		return nil, errClosed
	}
	if encrypted {
		key, err := c.protection.DeriveKey("media/" + c.account)
		if err != nil {
			return nil, err
		}
		f.key, f.alg = key, sealedfile.Fastest()
		f.nameKey, err = c.protection.DeriveKey("media-names/" + c.account)
		if err != nil {
			return nil, err
		}
	} else {
		f.alg, f.nameKey = sealedfile.Plain, []byte("komarugram media names")
	}
	if err := f.load(); err != nil {
		return nil, err
	}
	c.mediaFiles = f
	if !c.movingStarted {
		c.movingStarted = true
		c.moving.Go(c.moveLargeMedia)
	}
	return f, nil
}

// moveLargeMedia moves the large media the table kept before media files,
// a few at a time, to files, and drops the parts of videos it kept: a video
// streams into a file now.
func (c *Cache) moveLargeMedia() {
	c.mu.Lock()
	if !c.closed {
		var parts int64
		if err := c.db.QueryRow(`SELECT coalesce(sum(length(data)),0) FROM media WHERE key GLOB '*/range/*'`).Scan(&parts); err == nil && parts > 0 {
			if _, err := c.db.Exec(`DELETE FROM media WHERE key GLOB '*/range/*'`); err == nil && c.mediaBytes >= 0 {
				c.mediaBytes -= parts
			}
		}
	}
	c.mu.Unlock()
	for {
		select {
		case <-c.stop:
			return
		default:
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		var key string
		var data []byte
		err := c.db.QueryRow(`SELECT key, data FROM media WHERE length(data) > ? LIMIT 1`, smallMedia).Scan(&key, &data)
		c.mu.Unlock()
		if errors.Is(err, sql.ErrNoRows) {
			c.compact()
			return
		}
		if err != nil {
			return
		}
		if err := c.saveFileMedia(key, data); err != nil {
			return
		}
		c.mu.Lock()
		if !c.closed {
			if _, err = c.db.Exec(`DELETE FROM media WHERE key=?`, key); err == nil && c.mediaBytes >= 0 {
				c.mediaBytes -= int64(len(data))
			}
		}
		c.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// load reads the index, and removes the files it does not name and the
// entries whose file is gone.
func (f *mediaFiles) load() error {
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return err
	}
	c := f.c
	c.mu.Lock()
	rows, err := c.db.Query(`SELECT name, id, size, chunks, used FROM media_files`)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	for rows.Next() {
		e := &mediaEntry{}
		if err = rows.Scan(&e.name, &e.id, &e.size, &e.chunks, &e.used); err != nil {
			break
		}
		if e.size <= 0 || len(e.chunks) != (e.count()+7)/8 || e.name != f.nameOf(e.id) {
			// Another key's, or broken: removed with the files below.
			continue
		}
		for i := range e.count() {
			if e.has(i) {
				e.present += e.chunkLen(i)
			}
		}
		f.entries[e.id] = e
	}
	rows.Close()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, e := range f.entries {
		names[e.name] = true
	}
	found := map[string]bool{}
	_ = filepath.WalkDir(f.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// A segment is named <name>-<number>.
		name, _, _ := strings.Cut(d.Name(), "-")
		if !names[name] {
			_ = os.Remove(path)
		} else {
			found[name] = true
		}
		return nil
	})
	for id, e := range f.entries {
		if !found[e.name] {
			delete(f.entries, id)
			continue
		}
		// A segment gone is found when it is opened.
		f.bytes += e.present
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Entries of other keys, and of files gone.
	if _, err := c.db.Exec(`DELETE FROM media_files WHERE name NOT IN (SELECT value FROM json_each(?))`, jsonNames(f.entries)); err != nil {
		return err
	}
	return nil
}

func jsonNames(entries map[string]*mediaEntry) string {
	var b strings.Builder
	b.WriteByte('[')
	first := true
	for _, e := range entries {
		if !first {
			b.WriteByte(',')
		}
		first = false
		// Names are hex.
		b.WriteString(`"` + e.name + `"`)
	}
	b.WriteByte(']')
	return b.String()
}

// nameOf is the file name of medium id: a keyed hash, which tells nothing of
// the medium.
func (f *mediaFiles) nameOf(id string) string {
	h := hmac.New(sha256.New, f.nameKey)
	h.Write([]byte(id))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// pathOf is where segment k of the medium named name is.
func (f *mediaFiles) pathOf(name string, k int) string {
	return filepath.Join(f.dir, name[:2], name+"-"+strconv.Itoa(k))
}

// segment is segment k of e, opened; made when there is none and create is
// set, or nil. A segment gone, changed or not of e loses its chunks. It runs
// with mu held.
func (f *mediaFiles) segment(e *mediaEntry, k int, create bool) (*sealedfile.File, error) {
	if file := e.files[k]; file != nil {
		return file, nil
	}
	path, context := f.pathOf(e.name, k), e.name+"/"+strconv.Itoa(k)
	file, err := sealedfile.Open(path, f.key, context)
	if err == nil && (file.Size() != e.segmentSize(k) || file.Algorithm() != f.alg) {
		file.Close()
		file, err = nil, sealedfile.ErrCorrupt
	}
	if err != nil {
		f.forgetSegmentLocked(e, k)
		if !create {
			return nil, nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if file, err = sealedfile.Create(path, f.key, f.alg, e.segmentSize(k), context); err != nil {
			return nil, err
		}
	}
	if e.files == nil {
		e.files = map[int]*sealedfile.File{}
	}
	e.files[k] = file
	return file, nil
}

// forgetSegmentLocked marks the chunks of segment k of e missing. It runs
// with mu held.
func (f *mediaFiles) forgetSegmentLocked(e *mediaEntry, k int) {
	for i := k * segmentChunks; i < min(e.count(), (k+1)*segmentChunks); i++ {
		f.forgetLocked(e, i)
	}
}

// forgetLocked marks chunk i of e missing. It runs with mu held.
func (f *mediaFiles) forgetLocked(e *mediaEntry, i int) {
	if e.has(i) {
		e.chunks[i/8] &^= 1 << (i % 8)
		e.present -= e.chunkLen(i)
		f.bytes -= e.chunkLen(i)
		e.dirty = true
	}
}

// acquire is the entry of medium id, opened, and kept from eviction until
// release; a size other than its own starts the file over. A size of 0
// only finds an entry that is there.
func (f *mediaFiles) acquire(id string, size int64) (*mediaEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, errClosed
	}
	e := f.entries[id]
	if e != nil && size > 0 && e.size != size && e.refs == 0 {
		f.removeLocked(e)
		e = nil
	}
	if e == nil {
		if size <= 0 {
			return nil, nil
		}
		e = &mediaEntry{id: id, name: f.nameOf(id), size: size}
		e.chunks = make([]byte, (e.count()+7)/8)
		f.entries[id] = e
	}
	e.refs++
	if now := time.Now().UnixNano(); now-e.used > int64(time.Minute) {
		e.used, e.dirty = now, true
	}
	return e, nil
}

// release lets e be evicted again; the last to let go closes its files and
// saves its entry.
func (f *mediaFiles) release(e *mediaEntry) {
	f.mu.Lock()
	e.refs--
	last := e.refs == 0
	if last {
		e.closeFiles()
	}
	f.mu.Unlock()
	if last {
		f.flush()
	}
}

// read opens chunk i of e into buf.
func (f *mediaFiles) read(e *mediaEntry, i int, buf []byte) ([]byte, error) {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil, errClosed
	}
	var file *sealedfile.File
	var err error
	if e.has(i) {
		file, err = f.segment(e, i/segmentChunks, false)
	}
	ok := e.has(i)
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !ok || file == nil {
		return nil, ErrMissing
	}
	b, err := file.ReadChunk(i%segmentChunks, buf)
	if errors.Is(err, sealedfile.ErrCorrupt) {
		// A write cut short, or the file changed: the chunk is fetched
		// again.
		f.mu.Lock()
		f.forgetLocked(e, i)
		f.mu.Unlock()
		return nil, ErrMissing
	}
	return b, err
}

// write seals data, chunk i of e, into its file.
func (f *mediaFiles) write(e *mediaEntry, i int, data []byte) error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return errClosed
	}
	file, err := f.segment(e, i/segmentChunks, true)
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if err := file.WriteChunk(i%segmentChunks, data); err != nil {
		return err
	}
	f.mu.Lock()
	flush := false
	if !e.has(i) {
		e.chunks[i/8] |= 1 << (i % 8)
		e.present += int64(len(data))
		f.bytes += int64(len(data))
		e.dirty = true
		f.written++
		flush = f.written >= flushEvery || e.complete()
	}
	evicted := f.evictLocked()
	f.mu.Unlock()
	if flush || len(evicted) > 0 {
		f.flush()
	}
	return nil
}

// evictLocked removes the files used longest ago, of those no one uses now,
// until an eighth of the budget is free, when it is over. It runs with mu
// held.
func (f *mediaFiles) evictLocked() []string {
	if f.bytes <= f.budget {
		return nil
	}
	var idle []*mediaEntry
	for _, e := range f.entries {
		if e.refs == 0 {
			idle = append(idle, e)
		}
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].used < idle[j].used })
	var names []string
	for _, e := range idle {
		if f.bytes <= f.budget-f.budget/8 {
			break
		}
		names = append(names, e.name)
		f.removeLocked(e)
	}
	return names
}

// removeLocked removes e and its file; its entry goes with the next flush.
// It runs with mu held.
func (f *mediaFiles) removeLocked(e *mediaEntry) {
	e.closeFiles()
	for k := range e.segments() {
		_ = os.Remove(f.pathOf(e.name, k))
	}
	f.bytes -= e.present
	delete(f.entries, e.id)
}

// flush saves the entries changed, and drops those removed.
func (f *mediaFiles) flush() {
	type record struct {
		name, id   string
		size, used int64
		chunks     []byte
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	var records []record
	for _, e := range f.entries {
		if e.dirty {
			records = append(records, record{e.name, e.id, e.size, e.used, append([]byte(nil), e.chunks...)})
			e.dirty = false
		}
	}
	names := jsonNames(f.entries)
	f.written = 0
	f.mu.Unlock()
	c := f.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	tx, err := c.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	for _, r := range records {
		if _, err = tx.Exec(`INSERT OR REPLACE INTO media_files VALUES(?,?,?,?,?)`, r.name, r.id, r.size, r.chunks, r.used); err != nil {
			return
		}
	}
	if _, err = tx.Exec(`DELETE FROM media_files WHERE name NOT IN (SELECT value FROM json_each(?))`, names); err != nil {
		return
	}
	_ = tx.Commit()
}

// close saves the index and closes the files; nothing reads or writes them
// after.
func (f *mediaFiles) close() {
	f.flush()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for _, e := range f.entries {
		e.closeFiles()
	}
}

// drop removes every media file and its entry: their key, or their being
// sealed at all, no longer holds. It runs with the cache's mutex held.
func (f *mediaFiles) drop() error {
	f.mu.Lock()
	f.closed = true
	for _, e := range f.entries {
		e.closeFiles()
	}
	f.entries = nil
	f.mu.Unlock()
	if err := os.RemoveAll(f.dir); err != nil {
		return err
	}
	_, err := f.c.db.Exec(`DELETE FROM media_files`)
	return err
}

// MediaFile is a medium kept as a file, read and written by chunk, in any
// order and at once from several goroutines, while it is open.
type MediaFile struct {
	files *mediaFiles
	entry *mediaEntry
	once  sync.Once
}

// MediaFile opens the file of medium id, of size bytes, made if there is
// none; its chunks are written as they come.
func (c *Cache) MediaFile(ctx context.Context, id string, size int64) (*MediaFile, error) {
	if size <= 0 {
		return nil, fmt.Errorf("historycache: media of %d bytes", size)
	}
	files, err := c.files()
	if err != nil {
		return nil, err
	}
	e, err := files.acquire(id, size)
	if err != nil {
		return nil, err
	}
	return &MediaFile{files: files, entry: e}, nil
}

// Size is the size of the medium.
func (m *MediaFile) Size() int64 { return m.entry.size }

// Chunks is the number of chunks of the medium.
func (m *MediaFile) Chunks() int { return m.entry.count() }

// Has reports whether chunk i is there.
func (m *MediaFile) Has(i int) bool {
	m.files.mu.Lock()
	defer m.files.mu.Unlock()
	return m.entry.has(i)
}

// Complete reports whether every chunk is there.
func (m *MediaFile) Complete() bool {
	m.files.mu.Lock()
	defer m.files.mu.Unlock()
	return m.entry.complete()
}

// ReadChunk opens chunk i into buf; a chunk not there, or that does not
// open, is ErrMissing.
func (m *MediaFile) ReadChunk(i int, buf []byte) ([]byte, error) {
	return m.files.read(m.entry, i, buf)
}

// WriteChunk keeps data as chunk i: MediaChunk bytes, but for the last.
func (m *MediaFile) WriteChunk(i int, data []byte) error {
	if i < 0 || i >= m.entry.count() || int64(len(data)) != m.entry.chunkLen(i) {
		return fmt.Errorf("historycache: chunk %d of %d bytes", i, len(data))
	}
	return m.files.write(m.entry, i, data)
}

// Close lets the file be evicted again.
func (m *MediaFile) Close() error {
	m.once.Do(func() { m.files.release(m.entry) })
	return nil
}

// fileMedia is medium id whole from its file, or nil when it is not all
// there.
func (c *Cache) fileMedia(id string) ([]byte, error) {
	files, err := c.files()
	if err != nil {
		return nil, err
	}
	e, err := files.acquire(id, 0)
	if err != nil || e == nil {
		return nil, err
	}
	defer files.release(e)
	files.mu.Lock()
	complete := e.complete()
	files.mu.Unlock()
	if !complete {
		return nil, nil
	}
	out := make([]byte, 0, e.size)
	for i := range e.count() {
		b, err := files.read(e, i, out[len(out):cap(out)])
		if errors.Is(err, ErrMissing) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		out = out[:len(out)+len(b)]
	}
	return out, nil
}

// compact gives back to the disk the pages the database has free past
// compactFree, those of the media moved to files above all: once, rewriting
// it with auto_vacuum=INCREMENTAL (VACUUM), so that the free pages of later
// can be given back without rewriting it.
func (c *Cache) compact() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	var auto, free, page int64
	if c.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&auto) != nil ||
		c.db.QueryRow(`PRAGMA freelist_count`).Scan(&free) != nil ||
		c.db.QueryRow(`PRAGMA page_size`).Scan(&page) != nil || free*page < compactFree {
		return
	}
	if auto == 2 {
		_, _ = c.db.Exec(`PRAGMA incremental_vacuum`)
		return
	}
	_, _ = c.db.Exec(`PRAGMA auto_vacuum=INCREMENTAL; VACUUM`)
}

// compactFree is the free space of the database worth giving back.
const compactFree = 32 << 20

// saveFileMedia keeps b, medium id whole, as a file.
func (c *Cache) saveFileMedia(id string, b []byte) error {
	files, err := c.files()
	if err != nil {
		return err
	}
	e, err := files.acquire(id, int64(len(b)))
	if err != nil {
		return err
	}
	defer files.release(e)
	for i := range e.count() {
		start := int64(i) * MediaChunk
		if err := files.write(e, i, b[start:start+e.chunkLen(i)]); err != nil {
			return err
		}
	}
	return nil
}
