package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Scheduler runs background jobs for timed operations.
type Scheduler struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

// New creates a new scheduler.
func New(db *pgxpool.Pool, logger *slog.Logger) *Scheduler {
	return &Scheduler{db: db, logger: logger}
}

// Start begins all background goroutines. Returns a cancel func to stop them.
func (s *Scheduler) Start(ctx context.Context) {
	go s.runPublishScheduler(ctx)
}

// runPublishScheduler checks every minute for tracks/albums to publish.
func (s *Scheduler) runPublishScheduler(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	s.logger.Info("scheduler: release publisher started")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler: release publisher stopped")
			return
		case <-ticker.C:
			s.publishScheduledTracks(ctx)
			s.publishScheduledAlbums(ctx)
		}
	}
}

func (s *Scheduler) publishScheduledTracks(ctx context.Context) {
	cmd, err := s.db.Exec(ctx, `
		UPDATE tracks
		SET status = 'published', released_at = NOW(), updated_at = NOW()
		WHERE status = 'scheduled' AND scheduled_at <= NOW()
	`)
	if err != nil {
		s.logger.Error("scheduler: failed to publish scheduled tracks", "error", err)
		return
	}
	if n := cmd.RowsAffected(); n > 0 {
		s.logger.Info("scheduler: published scheduled tracks", "count", n)
	}
}

func (s *Scheduler) publishScheduledAlbums(ctx context.Context) {
	cmd, err := s.db.Exec(ctx, `
		UPDATE albums
		SET status = 'published', released_at = NOW(), updated_at = NOW()
		WHERE status = 'scheduled' AND scheduled_at <= NOW()
	`)
	if err != nil {
		s.logger.Error("scheduler: failed to publish scheduled albums", "error", err)
		return
	}
	if n := cmd.RowsAffected(); n > 0 {
		s.logger.Info("scheduler: published scheduled albums", "count", n)
	}
}
