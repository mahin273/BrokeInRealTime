package watcher

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mahin273/BrokeInRealTime/internal/aggregator"
	"github.com/mahin273/BrokeInRealTime/internal/config"
	"github.com/mahin273/BrokeInRealTime/internal/meta"
	"github.com/mahin273/BrokeInRealTime/internal/parser"
)

// Watcher manages file system monitoring and event ingestion.
type Watcher struct {
	baseDir    string // ~/.gemini/antigravity
	brainDir   string // ~/.gemini/antigravity/brain
	cfg        *config.Config
	agg        *aggregator.Aggregator
	metaRes    *meta.Resolver
	fsWatcher  *fsnotify.Watcher
	mu         sync.Mutex
	cursors    map[string]*parser.FileCursor
	watchedDirs map[string]bool
}

// NewWatcher creates a new Watcher instance.
func NewWatcher(baseDir string, cfg *config.Config, agg *aggregator.Aggregator, metaRes *meta.Resolver) (*Watcher, error) {
	brainDir := filepath.Join(baseDir, "brain")

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		baseDir:     baseDir,
		brainDir:    brainDir,
		cfg:         cfg,
		agg:         agg,
		metaRes:     metaRes,
		fsWatcher:   fsw,
		cursors:     make(map[string]*parser.FileCursor),
		watchedDirs: make(map[string]bool),
	}

	return w, nil
}

// Start begins monitoring the brain directory and spawns event processing loop.
func (w *Watcher) Start(ctx context.Context, backfill bool) error {
	// Ensure brain dir exists
	if _, err := os.Stat(w.brainDir); os.IsNotExist(err) {
		log.Printf("[Watcher] Brain directory %s does not exist yet. Watching base dir %s", w.brainDir, w.baseDir)
		_ = os.MkdirAll(w.brainDir, 0755)
	}

	// Add brainDir to watcher
	if err := w.watchDir(w.brainDir); err != nil {
		log.Printf("[Watcher] Warning: could not watch brain dir: %v", err)
	}

	// Initial scan of existing conversation directories
	w.scanExisting(backfill)

	// Event loop
	go w.runEventLoop(ctx)

	// Periodic poll fallback for any missed notifications (every 3 seconds)
	go w.runPeriodicPoll(ctx)

	return nil
}

// Stop closes the file watcher and cleanup.
func (w *Watcher) Stop() error {
	return w.fsWatcher.Close()
}

func (w *Watcher) watchDir(dir string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.watchedDirs[dir] {
		return nil
	}

	err := w.fsWatcher.Add(dir)
	if err == nil {
		w.watchedDirs[dir] = true
	}
	return err
}

func (w *Watcher) scanExisting(backfill bool) {
	entries, err := os.ReadDir(w.brainDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			convID := entry.Name()
			logsDir := filepath.Join(w.brainDir, convID, ".system_generated", "logs")
			transcriptFile := filepath.Join(logsDir, "transcript.jsonl")

			if _, err := os.Stat(logsDir); err == nil {
				_ = w.watchDir(logsDir)
			}

			if _, err := os.Stat(transcriptFile); err == nil {
				w.registerFile(transcriptFile, convID, backfill)
			}
		}
	}
}

func (w *Watcher) registerFile(filePath string, convID string, backfill bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.cursors[filePath]; exists {
		return
	}

	originSource := w.metaRes.ResolveSource(convID)
	cursor := &parser.FileCursor{
		Path:           filePath,
		Offset:         0,
		ConversationID: convID,
		OriginSource:   originSource,
		DefaultModel:   "gemini-3.8-flash",
	}

	if !backfill {
		// Jump to end of file if not backfilling
		if fi, err := os.Stat(filePath); err == nil {
			cursor.Offset = fi.Size()
		}
	}

	w.cursors[filePath] = cursor

	// Parse available content
	if backfill {
		w.processCursor(cursor)
	}
}

func (w *Watcher) processCursor(cursor *parser.FileCursor) {
	res, err := parser.ParseIncremental(cursor, w.cfg)
	if err != nil {
		return
	}

	if res.Cooling != nil {
		w.agg.UpdateQuota(res.Cooling)
	}

	for _, evt := range res.Events {
		w.agg.IngestEvent(evt)
	}
}

func (w *Watcher) runEventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			log.Printf("[Watcher] Error: %v", err)
		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}

			// If directory created inside brainDir, watch it
			if event.Has(fsnotify.Create) {
				fi, err := os.Stat(event.Name)
				if err == nil && fi.IsDir() {
					_ = w.watchDir(event.Name)

					// Check if this is a newly created .system_generated/logs
					if strings.HasSuffix(event.Name, ".system_generated/logs") {
						parts := strings.Split(event.Name, string(filepath.Separator))
						if len(parts) >= 3 {
							convID := parts[len(parts)-3]
							transcriptFile := filepath.Join(event.Name, "transcript.jsonl")
							w.registerFile(transcriptFile, convID, true)
						}
					}
				}
			}

			// If transcript.jsonl written or created
			if strings.HasSuffix(event.Name, "transcript.jsonl") {
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					w.handleFileWrite(event.Name)
				}
			}
		}
	}
}

func (w *Watcher) handleFileWrite(filePath string) {
	w.mu.Lock()
	cursor, exists := w.cursors[filePath]
	if !exists {
		// Try discovering conversation ID from path
		dir := filepath.Dir(filePath) // .system_generated/logs
		parent := filepath.Dir(filepath.Dir(dir))
		convID := filepath.Base(parent)

		originSource := w.metaRes.ResolveSource(convID)
		cursor = &parser.FileCursor{
			Path:           filePath,
			Offset:         0,
			ConversationID: convID,
			OriginSource:   originSource,
			DefaultModel:   "gemini-3.8-flash",
		}
		w.cursors[filePath] = cursor
	}
	w.mu.Unlock()

	w.mu.Lock()
	w.processCursor(cursor)
	w.mu.Unlock()
}

func (w *Watcher) runPeriodicPoll(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Scan for any new files or unread lines
			w.scanExisting(true)

			w.mu.Lock()
			for _, cursor := range w.cursors {
				w.processCursor(cursor)
			}
			w.mu.Unlock()
		}
	}
}
