package server

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// JobKind identifies what a job is doing — purely informational, the actual
// work is done by handlers that know their type.
type JobKind string

const (
	JobBench   JobKind = "bench"
	JobExplain JobKind = "explain"
	JobAdvise  JobKind = "advise"
)

// JobStatus is one of the lifecycle states a Job moves through.
type JobStatus string

const (
	JobPending  JobStatus = "pending"
	JobRunning  JobStatus = "running"
	JobDone     JobStatus = "done"
	JobErrored  JobStatus = "errored"
	JobCanceled JobStatus = "canceled"
)

// Job is the in-memory record of a server-launched task. We don't persist
// jobs to the DB — they're ephemeral and live only as long as the server
// process. RunID is set once the underlying run is created in the store.
type Job struct {
	ID         string
	Kind       JobKind
	ProjectID  int64
	RunID      int64
	Status     JobStatus
	StartedAt  time.Time
	FinishedAt time.Time
	Done       int
	Total      int
	Message    string
	Err        string
	cancel     context.CancelFunc
}

// JobManager is the concurrent registry of in-flight jobs plus a tiny
// pub-sub for SSE consumers.
type JobManager struct {
	mu      sync.RWMutex
	jobs    map[string]*Job
	seq     uint64
	subs    map[chan JobEvent]struct{}
	subsMu  sync.RWMutex
}

// JobEvent is what SSE clients receive.
type JobEvent struct {
	JobID     string    `json:"job_id"`
	Kind      JobKind   `json:"kind"`
	Status    JobStatus `json:"status"`
	ProjectID int64     `json:"project_id"`
	RunID     int64     `json:"run_id,omitempty"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	Message   string    `json:"message,omitempty"`
	Err       string    `json:"err,omitempty"`
}

// NewJobManager builds an empty manager.
func NewJobManager() *JobManager {
	return &JobManager{
		jobs: map[string]*Job{},
		subs: map[chan JobEvent]struct{}{},
	}
}

// Start records a new job and returns its id + a context the worker should
// honor. The worker drives the job's progress via Update().
func (m *JobManager) Start(kind JobKind, projectID int64) (*Job, context.Context) {
	id := nextID(&m.seq)
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{
		ID:        id,
		Kind:      kind,
		ProjectID: projectID,
		Status:    JobRunning,
		StartedAt: time.Now(),
		cancel:    cancel,
	}
	m.mu.Lock()
	m.jobs[id] = j
	m.mu.Unlock()
	m.broadcast(j)
	return j, ctx
}

// Update mutates a job's progress and broadcasts. Pass -1 to leave a value
// unchanged.
func (m *JobManager) Update(jobID string, done, total int, message string) {
	m.mu.Lock()
	j := m.jobs[jobID]
	if j != nil {
		if done >= 0 {
			j.Done = done
		}
		if total >= 0 {
			j.Total = total
		}
		if message != "" {
			j.Message = message
		}
	}
	m.mu.Unlock()
	if j != nil {
		m.broadcast(j)
	}
}

// SetRunID is called by the worker as soon as the underlying run is created.
func (m *JobManager) SetRunID(jobID string, runID int64) {
	m.mu.Lock()
	j := m.jobs[jobID]
	if j != nil {
		j.RunID = runID
	}
	m.mu.Unlock()
	if j != nil {
		m.broadcast(j)
	}
}

// Finish marks a job as done or errored.
func (m *JobManager) Finish(jobID string, status JobStatus, errMsg string) {
	m.mu.Lock()
	j := m.jobs[jobID]
	if j != nil {
		j.Status = status
		j.Err = errMsg
		j.FinishedAt = time.Now()
	}
	m.mu.Unlock()
	if j != nil {
		m.broadcast(j)
	}
}

// Cancel signals the job's context to abort.
func (m *JobManager) Cancel(jobID string) bool {
	m.mu.RLock()
	j := m.jobs[jobID]
	m.mu.RUnlock()
	if j == nil || j.cancel == nil {
		return false
	}
	j.cancel()
	return true
}

// List returns a snapshot of all known jobs, newest-first.
func (m *JobManager) List() []Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	// sort newest-first by StartedAt
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].StartedAt.After(out[j-1].StartedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Subscribe returns a channel that receives every job event until the
// returned unsubscribe func is called.
func (m *JobManager) Subscribe() (<-chan JobEvent, func()) {
	ch := make(chan JobEvent, 32)
	m.subsMu.Lock()
	m.subs[ch] = struct{}{}
	m.subsMu.Unlock()
	return ch, func() {
		m.subsMu.Lock()
		delete(m.subs, ch)
		m.subsMu.Unlock()
		close(ch)
	}
}

func (m *JobManager) broadcast(j *Job) {
	ev := JobEvent{
		JobID: j.ID, Kind: j.Kind, Status: j.Status,
		ProjectID: j.ProjectID, RunID: j.RunID,
		Done: j.Done, Total: j.Total, Message: j.Message, Err: j.Err,
	}
	m.subsMu.RLock()
	defer m.subsMu.RUnlock()
	for ch := range m.subs {
		select {
		case ch <- ev:
		default:
			// drop if subscriber is slow — better than blocking
		}
	}
}

func nextID(seq *uint64) string {
	n := atomic.AddUint64(seq, 1)
	// short hex id; collision-free within a single process lifetime
	return formatHex(n)
}

func formatHex(n uint64) string {
	const hex = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = hex[n&0xf]
		n >>= 4
	}
	return string(b[i:])
}
