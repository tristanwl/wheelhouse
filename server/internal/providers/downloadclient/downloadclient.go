// Package downloadclient is the download-client role: the normalized model every
// download client (SABnzbd, qBittorrent) is mapped onto, the Client interface a
// provider's Create function returns, and RunContract, the reusable test suite
// each adapter must pass. It is the Go port of lib/providers/download-client
// (types.ts and contract.ts).
//
// Normalization rules: sizes in bytes, progress 0..1, durations in seconds,
// states from the shared State enum. User-facing text never names
// configuration keys or environment variables.
package downloadclient

import (
	"context"
	"math"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// Protocol is how a client downloads.
type Protocol string

// The protocols.
const (
	Usenet  Protocol = "usenet"
	Torrent Protocol = "torrent"
)

// State is the shared download state enum.
type State string

// The states a download can be in.
const (
	StateQueued         State = "queued"
	StateDownloading    State = "downloading"
	StateStalled        State = "stalled"
	StatePostprocessing State = "postprocessing"
	StateSeeding        State = "seeding"
	StatePaused         State = "paused"
	StateCompleted      State = "completed"
	StateFailed         State = "failed"
)

// States lists every State.
var States = []State{
	StateQueued, StateDownloading, StateStalled, StatePostprocessing,
	StateSeeding, StatePaused, StateCompleted, StateFailed,
}

// TorrentInfo is the torrent-only part of an Item.
type TorrentInfo struct {
	Ratio             float64 `json:"ratio"`
	Seeders           int64   `json:"seeders"`
	UploadBytesPerSec int64   `json:"uploadBytesPerSec"`
}

// Item is one download, normalized.
type Item struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Protocol Protocol `json:"protocol"`
	State    State    `json:"state"`
	// StateDetail is the client's own wording where it adds information:
	// "Extracting", "Checking", "Grabbing".
	StateDetail string `json:"stateDetail,omitempty"`
	// Progress is 0..1.
	Progress       float64 `json:"progress"`
	SizeBytes      int64   `json:"sizeBytes"`
	RemainingBytes int64   `json:"remainingBytes"`
	// ETASeconds is nil when unknown (stalled, paused, or the client does not say).
	ETASeconds *int64       `json:"etaSeconds"`
	Torrent    *TorrentInfo `json:"torrent,omitempty"`
}

// Warning is something the client itself reported.
type Warning struct {
	Message string `json:"message"`
	// Severity is "warning" or "error".
	Severity string `json:"severity"`
	// At is an ISO 8601 timestamp, when the client reports one.
	At string `json:"at,omitempty"`
}

// Warning severities.
const (
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// UsenetServer is one news server a usenet client is configured with.
type UsenetServer struct {
	Name        string `json:"name"`
	Connections int64  `json:"connections"`
	SSL         bool   `json:"ssl"`
	Enabled     bool   `json:"enabled"`
}

// UsenetStatus is the usenet-only part of a Status.
type UsenetStatus struct {
	Servers []UsenetServer `json:"servers"`
}

// TorrentStatus is the torrent-only part of a Status.
type TorrentStatus struct {
	Seeding           int64 `json:"seeding"`
	UploadBytesPerSec int64 `json:"uploadBytesPerSec"`
}

// Status is the client's overall state.
type Status struct {
	Paused bool `json:"paused"`
	// StatusText is the client's own one-word status, e.g. SABnzbd's "Idle" or
	// "Downloading".
	StatusText       string `json:"statusText,omitempty"`
	SpeedBytesPerSec int64  `json:"speedBytesPerSec"`
	QueueCount       int64  `json:"queueCount"`
	// ETASeconds is the time left for the whole queue; nil when unknown or
	// nothing is queued.
	ETASeconds *int64 `json:"etaSeconds"`
	Version    string `json:"version,omitempty"`
	// Warnings is never nil, so it marshals as [].
	Warnings []Warning      `json:"warnings"`
	Usenet   *UsenetStatus  `json:"usenet,omitempty"`
	Torrent  *TorrentStatus `json:"torrent,omitempty"`
}

// Capabilities is static, per provider: it lets the UI decide what to render
// without calling the service. It is the core.Definition's Capabilities value.
type Capabilities struct {
	Protocol      Protocol `json:"protocol"`
	CanPause      bool     `json:"canPause"`
	UsenetServers bool     `json:"usenetServers"`
	Seeding       bool     `json:"seeding"`
}

// Client is what a download-client provider's Create function returns (as the
// any of core.Definition.Create).
//
// TestConnection never returns an error: a failure is reported in the result,
// already redacted. Status and Items return a *core.ProviderError (safe to show
// and to log) when the service cannot be used.
type Client interface {
	TestConnection(ctx context.Context) core.ConnectionResult
	Status(ctx context.Context) (Status, error)
	Items(ctx context.Context) ([]Item, error)
}

// DescribeState is the one-line label for an item's state, for list rows.
func DescribeState(state State, detail string) string {
	if state == StatePostprocessing {
		if detail != "" {
			return detail
		}
		return "Processing"
	}
	if detail != "" {
		return detail
	}
	switch state {
	case StateQueued:
		return "Queued"
	case StateDownloading:
		return "Downloading"
	case StateStalled:
		return "Stalled"
	case StateSeeding:
		return "Seeding"
	case StatePaused:
		return "Paused"
	case StateCompleted:
		return "Completed"
	case StateFailed:
		return "Failed"
	}
	return string(state)
}

// Failed returns a Client whose every call reports err. A provider's Create
// function has no way to fail, so it returns this when it cannot build a real
// client (which validated config should make impossible).
func Failed(err error) Client { return failedClient{err} }

type failedClient struct{ err error }

func (f failedClient) TestConnection(context.Context) core.ConnectionResult {
	return core.ConnectionResult{Error: ErrorText(f.err)}
}
func (f failedClient) Status(context.Context) (Status, error) { return Status{}, f.err }
func (f failedClient) Items(context.Context) ([]Item, error)  { return nil, f.err }

// ErrorText is the text of a ProviderError, or a fixed phrase for any other
// error (so nothing unredacted can slip into a connection result).
func ErrorText(err error) string {
	if pe, ok := errorAsProvider(err); ok {
		return pe.Message
	}
	return "unknown error"
}

// RoundHalfUp rounds like JavaScript's Math.round (halves go up), which the
// TypeScript adapters rely on.
func RoundHalfUp(x float64) float64 { return math.Floor(x + 0.5) }

// Int64Ptr returns a pointer to n.
func Int64Ptr(n int64) *int64 { return &n }

// MaxSafe is the largest integer both languages represent exactly; byte counts
// above it are clamped (nobody has a 9 PB torrent, but a broken service can say so).
const MaxSafe = 1 << 53
