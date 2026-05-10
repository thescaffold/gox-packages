// Package batch ports ntx-packages/libs/core/src/services/batch.service.ts.
// BatchService.Run iterates a paginated dataset, locking each page via
// SyncService so workers don't double-process.
package batch

import (
	"fmt"
	"math"
	"time"

	syncsvc "github.com/thescaffold/gox-packages/libs/core/services/sync"
)

// Contract describes a paginated batch source. Mirrors TS BatchContract<T>.
// Implementations supply the dataset, pagination, and per-record handler.
type Contract interface {
	Name() string
	Length() (int, error)
	Limit() (int, error)
	Page(page, perPage int) ([]any, error)
	Handler(page, index int, record any) func() error
	Timeout() (time.Duration, error)
	Release() (bool, error)
	Repeat() (bool, error)
}

// Service runs a Contract: pages through, dispatches per-record handlers, locks
// each page so no two workers process it simultaneously.
type Service struct {
	sync *syncsvc.Service
}

// New constructs a BatchService backed by SyncService.
func New(sync *syncsvc.Service) *Service { return &Service{sync: sync} }

// Run executes the batch. Mirrors TS BatchService.run().
func (s *Service) Run(c Contract) error {
	total, err := c.Length()
	if err != nil {
		return err
	}
	perPage, err := c.Limit()
	if err != nil {
		return err
	}
	if perPage <= 0 {
		return fmt.Errorf("batch: limit must be > 0")
	}
	timeout, err := c.Timeout()
	if err != nil {
		return err
	}
	release, err := c.Release()
	if err != nil {
		return err
	}
	repeat, err := c.Repeat()
	if err != nil {
		return err
	}

	pages := int(math.Ceil(float64(total) / float64(perPage)))
	for i := 0; i < pages; i++ {
		page := i
		key := fmt.Sprintf("core:batch:%s:%d", c.Name(), page)
		err := s.sync.Once(key, func() error {
			records, err := c.Page(page, perPage)
			if err != nil {
				return err
			}
			// Dispatch concurrently, mirroring TS Promise.all.
			errs := make(chan error, len(records))
			for j, r := range records {
				j, r := j, r
				go func() { errs <- c.Handler(page, j, r)() }()
			}
			for range records {
				if e := <-errs; e != nil {
					return e
				}
			}
			return nil
		}, timeout, release, repeat)
		if err != nil {
			return err
		}
	}
	return nil
}
