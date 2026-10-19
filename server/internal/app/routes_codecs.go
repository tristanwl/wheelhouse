package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"

	"github.com/tristanwl/wheelhouse/server/internal/codecs"
	"github.com/tristanwl/wheelhouse/server/internal/converter"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

// ConverterOptions configures the codec routes. The zero value is the
// production behaviour.
type ConverterOptions struct {
	// MediaRoots are the folders conversion may touch (default
	// converter.DefaultMediaRoots).
	MediaRoots []string
	// ConfigDir holds settings.json (default codecs.DefaultConfigDir).
	ConfigDir string
	// Ffmpeg and Ffprobe are the binaries (default: found on PATH).
	Ffmpeg, Ffprobe string
	// Queue replaces the in-memory job queue.
	Queue converter.Queue
}

// maxBody bounds the JSON bodies of the codec routes.
const maxBody = 64 << 10

// readObject decodes a request body that must be a JSON object; ok is false
// for anything else.
func readObject(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return nil, false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	obj, ok := v.(map[string]any)
	return obj, ok
}

func str(obj map[string]any, key string) (string, bool) {
	s, ok := obj[key].(string)
	return s, ok
}

func num(obj map[string]any, key string) (float64, bool) {
	f, ok := obj[key].(float64)
	return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// registerCodecs registers the codec analysis, settings, encoder detection and
// conversion routes (app/api/codec-analysis/** in the Next app).
func registerCodecs(r *Router, d deps) {
	conv := d.conv
	if len(conv.MediaRoots) == 0 {
		conv.MediaRoots = converter.DefaultMediaRoots
	}
	if conv.ConfigDir == "" {
		conv.ConfigDir = codecs.DefaultConfigDir
	}
	store := codecs.NewStore(conv.ConfigDir)
	detector := &converter.Detector{Ffmpeg: conv.Ffmpeg}
	queue := conv.Queue
	if queue == nil {
		queue = converter.NewMemoryQueue(d.ctx, converter.Config{
			MediaRoots: conv.MediaRoots,
			Ffmpeg:     conv.Ffmpeg,
			Ffprobe:    conv.Ffprobe,
			Rescan: func(ctx context.Context, instanceID string, itemID int) error {
				client, err := d.rt.Client(ctx, instanceID)
				if err != nil {
					return err
				}
				m, ok := client.(mediamanager.Manager)
				if !ok {
					return fmt.Errorf("not a media manager")
				}
				return m.Rescan(ctx, itemID)
			},
		})
	}

	r.HandleFunc("GET /api/codec-analysis", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		kind := mediamanager.KindMovies
		if req.URL.Query().Get("kind") == "series" {
			kind = mediamanager.KindSeries
		}
		entries, err := mediamanager.ListManagers(req.Context(), d.rt, kind)
		if err != nil {
			internalError(w)
			return
		}
		if len(entries) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": mediamanager.NoManagerMessage(kind)})
			return
		}
		results, problems := mediamanager.CollectFromManagers(req.Context(), entries,
			func(ctx context.Context, client mediamanager.Manager, entry mediamanager.Entry) (codecs.Scan, error) {
				return codecs.ScanFiles(ctx, client, entry)
			})
		if len(results) == 0 && len(problems) > 0 {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": problems[0].Error, "problems": problems})
			return
		}
		files := []codecs.FileInfo{}
		resp := codecs.Response{}
		for _, res := range results {
			files = append(files, res.Value.Files...)
			resp.SkippedCount += res.Value.Skipped
		}
		resp.Analysis = codecs.Analyze(files, store.Get())
		resp.Problems = problems
		writeJSON(w, http.StatusOK, resp)
	})

	r.HandleFunc("GET /api/codec-analysis/settings", Authenticated, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, store.Get())
	})

	r.HandleFunc("PUT /api/codec-analysis/settings", Admin, func(w http.ResponseWriter, req *http.Request) {
		obj, ok := readObject(w, req)
		hd, hdOK := str(obj, "hd")
		uhd, uhdOK := str(obj, "uhd")
		prefs := codecs.Preferences{HD: codecs.Preferable(hd), UHD: codecs.Preferable(uhd)}
		if !ok || !hdOK || !uhdOK || !prefs.HD.Valid() || !prefs.UHD.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hd and uhd must each be 'h264' or 'hevc'"})
			return
		}
		if err := store.Set(prefs); err != nil {
			// Almost always a /config volume the container user can't write to.
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("Could not save settings (%s). Make sure the /config volume exists on the host and is writable by the container's user.", core.Redact(err.Error())),
			})
			return
		}
		writeJSON(w, http.StatusOK, store.Get())
	})

	r.HandleFunc("GET /api/codec-analysis/encoders", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"encoders": detector.Detect(req.Context())})
	})

	r.HandleFunc("GET /api/codec-analysis/convert", Authenticated, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": queue.List()})
	})

	r.HandleFunc("POST /api/codec-analysis/convert", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		badBody := func() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
		obj, ok := readObject(w, req)
		if !ok {
			badBody()
			return
		}
		instanceID, ok1 := str(obj, "instanceId")
		itemID, ok2 := num(obj, "itemId")
		filePath, ok3 := str(obj, "filePath")
		title, ok4 := str(obj, "title")
		originalSize, ok5 := num(obj, "originalSize")
		backendS, ok6 := str(obj, "backend")
		familyS, ok7 := str(obj, "targetFamily")
		backend, family := converter.Backend(backendS), converter.Family(familyS)
		if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !ok7 || !backend.Valid() || !family.Valid() ||
			itemID != math.Trunc(itemID) || math.Abs(itemID) > 1<<31 {
			badBody()
			return
		}

		// Nothing is read, queued or written for a path outside the media roots.
		resolved, err := converter.ResolveInRoots(conv.MediaRoots, filePath)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		// A library file with other hard links may be the copy a torrent client is
		// seeding. Converting leaves that copy alone (so disk use grows), or breaks
		// the seed if the link was relied on: make the user say they know. A file
		// that already has a job just reattaches, so it isn't asked again.
		if ack, _ := obj["acknowledgeHardlink"].(bool); !ack {
			if _, active := queue.FindActive(resolved); !active {
				if links, ok := converter.LinkCount(resolved); ok && links > 1 {
					writeJSON(w, http.StatusConflict, map[string]any{
						"error": fmt.Sprintf("This file has %d hard links, so another copy of it (for example one a torrent client is seeding) may share its data.", links),
						"code":  "hardlinked", "linkCount": links,
					})
					return
				}
			}
		}

		job := queue.Enqueue(converter.Request{
			InstanceID:   instanceID,
			ItemID:       int(itemID),
			FilePath:     filePath,
			Title:        title,
			OriginalSize: originalSize,
			Backend:      backend,
			TargetFamily: family,
			ResolvedPath: resolved,
		})
		d.audit(req, "conversion.start", job.ID, map[string]any{
			"instanceId": instanceID, "itemId": int(itemID), "title": clip(title, 200), "file": clip(filePath, 400),
			"backend": backendS, "targetFamily": familyS,
		})
		writeJSON(w, http.StatusOK, job)
	})

	r.HandleFunc("GET /api/codec-analysis/convert/{jobId}", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		job, ok := queue.Get(req.PathValue("jobId"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
			return
		}
		writeJSON(w, http.StatusOK, job)
	})

	r.HandleFunc("POST /api/codec-analysis/convert/{jobId}/cancel", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("jobId")
		// An unknown job is 404; a known one that already finished (or is being
		// cancelled) cannot be cancelled and stays 400.
		if _, known := queue.Get(id); !known {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
			return
		}
		if !queue.Cancel(id) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "job is not cancelable"})
			return
		}
		job, _ := queue.Get(id)
		d.audit(req, "conversion.cancel", id, map[string]any{"instanceId": job.InstanceID, "itemId": job.ItemID})
		writeJSON(w, http.StatusOK, job)
	})
}

// clip shortens text recorded in the audit log, on a character boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	for len(string(r)) > n {
		r = r[:len(r)-1]
	}
	return string(r)
}
