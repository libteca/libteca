package resourcebudget

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var ErrLimit = errors.New("resource budget limit exceeded")

type LimitError struct {
	Resource         string
	Limit, Requested int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%s: %s cap %d, requested %d", ErrLimit, e.Resource, e.Limit, e.Requested)
}
func (e *LimitError) Unwrap() error { return ErrLimit }

type Budget struct {
	mu          sync.Mutex
	name        string
	limit, used int64
}
type Reservation struct {
	b    *Budget
	n    int64
	once sync.Once
}

func New(name string, limit int64) *Budget { return &Budget{name: name, limit: limit} }
func (b *Budget) Reserve(n int64) (*Reservation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 0 || n > int64(^uint64(0)>>1)-b.used || (b.limit > 0 && n > b.limit-b.used) {
		return nil, &LimitError{b.name, b.limit, n}
	}
	b.used += n
	return &Reservation{b: b, n: n}, nil
}
func (r *Reservation) Release() {
	if r != nil {
		r.once.Do(func() { r.b.mu.Lock(); r.b.used -= r.n; r.b.mu.Unlock() })
	}
}
func Env(name string) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a nonnegative byte count", name)
	}
	return n, nil
}

func Read(r io.Reader, max int64, b *Budget) ([]byte, error) {
	data, reservation, err := ReadReserved(r, max, b)
	reservation.Release()
	return data, err
}
func ReadReserved(r io.Reader, max int64, b *Budget) ([]byte, *Reservation, error) {
	if max < 0 || max >= int64(int(^uint(0)>>1)) {
		return nil, nil, ErrLimit
	}
	reservation, err := b.Reserve(max + 1)
	if err != nil {
		return nil, nil, err
	}
	if b.limit == 0 {
		data, readErr := io.ReadAll(io.LimitReader(r, max+1))
		if readErr != nil {
			reservation.Release()
			return nil, nil, readErr
		}
		if int64(len(data)) > max {
			reservation.Release()
			return nil, nil, &LimitError{"archive entry", max, int64(len(data))}
		}
		return data, reservation, nil
	}
	data := make([]byte, int(max+1))
	n, err := io.ReadFull(r, data)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		reservation.Release()
		return nil, nil, err
	}
	if int64(n) > max {
		reservation.Release()
		return nil, nil, &LimitError{"archive entry", max, int64(n)}
	}
	return data[:n], reservation, nil
}

func Size(dir string) (int64, error) {
	var size int64
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("resource output is not regular: %s", path)
		}
		if info.Size() > int64(^uint64(0)>>1)-size {
			return ErrLimit
		}
		size += info.Size()
		return nil
	})
	return size, err
}

func Monitor(ctx context.Context, dir string, limit int64, cancel context.CancelFunc) func() error {
	done := make(chan struct{})
	stopped := make(chan struct{})
	var result error
	check := func() error {
		n, err := Size(dir)
		if err != nil {
			return err
		}
		if limit > 0 && n > limit {
			return &LimitError{"temporary disk", limit, n}
		}
		return nil
	}
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				result = check()
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := check(); err != nil {
					result = err
					cancel()
					return
				}
			}
		}
	}()
	return func() error { close(done); <-stopped; return result }
}

type DiskPool struct {
	mu                   sync.Mutex
	root                 string
	per, total, reserved int64
	active               map[*DiskLease]string
}
type DiskLease struct {
	pool  *DiskPool
	limit int64
	once  sync.Once
}

func NewDiskPool(root string, per, total int64) *DiskPool {
	return &DiskPool{root: root, per: per, total: total, active: map[*DiskLease]string{}}
}
func (p *DiskPool) Acquire(dir string) (*DiskLease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	limit := p.per
	if limit == 0 && p.total > 0 {
		limit = p.total
	}
	if p.total > 0 {
		used, err := Size(p.root)
		if err != nil {
			return nil, err
		}
		for lease, path := range p.active {
			n, err := Size(path)
			if err != nil {
				return nil, err
			}
			if n > lease.limit {
				n = lease.limit
			}
			used -= n
		}
		if used < 0 {
			used = 0
		}
		if used > p.total || p.reserved > p.total-used || limit > p.total-used-p.reserved {
			return nil, &LimitError{"aggregate disk", p.total, limit}
		}
	}
	p.reserved += limit
	lease := &DiskLease{pool: p, limit: limit}
	p.active[lease] = dir
	return lease, nil
}
func (l *DiskLease) Limit() int64 { return l.limit }
func (l *DiskLease) Release() {
	if l != nil {
		l.once.Do(func() { l.pool.mu.Lock(); l.pool.reserved -= l.limit; delete(l.pool.active, l); l.pool.mu.Unlock() })
	}
}

var configuredMu sync.Mutex
var configuredPools = map[string]*Budget{}

func ConfiguredBudget(env, resource string) (*Budget, error) {
	limit, err := Env(env)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s/%d", env, limit)
	configuredMu.Lock()
	defer configuredMu.Unlock()
	if b := configuredPools[key]; b != nil {
		return b, nil
	}
	b := New(resource, limit)
	configuredPools[key] = b
	return b, nil
}
func ArchiveBudget() (*Budget, error) {
	return ConfiguredBudget("LIBTECA_ARCHIVE_MEMORY_BYTES", "archive retained bytes")
}
