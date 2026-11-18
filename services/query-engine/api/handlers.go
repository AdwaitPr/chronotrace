package api

import (
	"math"
	"strconv"
	"time"

	"github.com/chronotrace/chronotrace/services/query-engine/timetravel"
	"github.com/chronotrace/chronotrace/shared/store"
	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/gofiber/fiber/v2"
)

// WaveformBucket represents an aggregated metric bucket for DAW timeline rendering.
type WaveformBucket struct {
	TimestampMs int64   `json:"timestamp_ms"`
	RPS         float64 `json:"rps"`
	ErrorRate   float64 `json:"error_rate"`
	P50Ms       float64 `json:"p50_ms"`
	P95Ms       float64 `json:"p95_ms"`
	P99Ms       float64 `json:"p99_ms"`
}

// IncidentHighlight represents an auto-detected incident region on the timeline.
type IncidentHighlight struct {
	StartMs     int64  `json:"start_ms"`
	EndMs       int64  `json:"end_ms"`
	Severity    string `json:"severity"` // critical, degraded
	Title       string `json:"title"`
	Culprit     string `json:"culprit"`
	Description string `json:"description"`
}

// TimelineOverviewResponse holds all data needed for the 4-track DAW timeline.
type TimelineOverviewResponse struct {
	FromMs     int64               `json:"from_ms"`
	ToMs       int64               `json:"to_ms"`
	Buckets    []WaveformBucket    `json:"buckets"`
	Deploys    []*types.DeployEvent `json:"deploys"`
	Highlights []IncidentHighlight `json:"highlights"`
}

// RegisterRoutes registers REST endpoints for timeline overview, state reconstruction, and metadata.
func RegisterRoutes(app *fiber.App, st store.TelemetryStore, rec *timetravel.StateReconstructor, meta store.MetadataStore) {
	api := app.Group("/api")

	// GET /api/timeline/overview
	api.Get("/timeline/overview", func(c *fiber.Ctx) error {
		nowMs := time.Now().UnixMilli()
		fromMs := parseQueryInt64(c, "from", nowMs-15*60*1000) // Default 15 minutes
		toMs := parseQueryInt64(c, "to", nowMs)
		numBuckets := parseQueryInt(c, "buckets", 300)

		if toMs <= fromMs {
			toMs = fromMs + 1000
		}
		if numBuckets <= 0 || numBuckets > 1000 {
			numBuckets = 300
		}

		ctx := c.Context()
		fromNano := fromMs * 1e6
		toNano := toMs * 1e6

		// Query deploy events
		deploys, err := st.GetDeployEvents(ctx, fromNano, toNano)
		if err != nil {
			deploys = []*types.DeployEvent{}
		}

		// Pre-bucket waveform metrics
		spanRange := toMs - fromMs
		bucketDurationMs := spanRange / int64(numBuckets)
		if bucketDurationMs < 100 {
			bucketDurationMs = 100
		}

		buckets := make([]WaveformBucket, numBuckets)
		var highlights []IncidentHighlight
		var activeIncident *IncidentHighlight

		for i := 0; i < numBuckets; i++ {
			bStartMs := fromMs + int64(i)*bucketDurationMs
			bEndMs := bStartMs + bucketDurationMs
			bMidNano := ((bStartMs + bEndMs) / 2) * 1e6

			// Quick state sample for this bucket
			pState, _ := rec.ReconstructState(ctx, bMidNano, int(bucketDurationMs/1000)+2)

			p50 := 25.0
			p95 := 45.0
			p99 := 85.0
			errRate := 0.0
			rps := 100.0

			if pState != nil {
				rps = pState.TotalThroughput
				errRate = pState.OverallErrorRate
				// Calculate max p95 among active nodes
				for _, n := range pState.Nodes {
					if n.P95LatencyMs > p95 {
						p95 = n.P95LatencyMs
						p99 = n.P95LatencyMs * 1.4
					}
				}
			}

			buckets[i] = WaveformBucket{
				TimestampMs: bStartMs,
				RPS:         math.Round(rps*10) / 10,
				ErrorRate:   math.Round(errRate*1000) / 1000,
				P50Ms:       math.Round(p50*10) / 10,
				P95Ms:       math.Round(p95*10) / 10,
				P99Ms:       math.Round(p99*10) / 10,
			}

			// Incident detection logic: p95 > 1000ms or error_rate > 5%
			isAnomaly := p95 > 1000.0 || errRate > 0.05
			if isAnomaly {
				if activeIncident == nil {
					culprit := "unknown"
					if pState != nil && len(pState.Suspects) > 0 {
						culprit = pState.Suspects[0].ServiceName
					}
					activeIncident = &IncidentHighlight{
						StartMs:     bStartMs,
						EndMs:       bEndMs,
						Severity:    "critical",
						Title:       "High Latency & 5xx Outage",
						Culprit:     culprit,
						Description: "Cascading failure detected across service dependencies",
					}
				} else {
					activeIncident.EndMs = bEndMs
				}
			} else {
				if activeIncident != nil {
					highlights = append(highlights, *activeIncident)
					activeIncident = nil
				}
			}
		}

		if activeIncident != nil {
			highlights = append(highlights, *activeIncident)
		}

		return c.JSON(TimelineOverviewResponse{
			FromMs:     fromMs,
			ToMs:       toMs,
			Buckets:    buckets,
			Deploys:    deploys,
			Highlights: highlights,
		})
	})

	// GET /api/state?ts=<unix_ms>&service=<optional_filter>
	api.Get("/state", func(c *fiber.Ctx) error {
		tsMs := parseQueryInt64(c, "ts", time.Now().UnixMilli())
		svcFilter := c.Query("service")
		ctx := c.Context()

		state, err := rec.ReconstructState(ctx, tsMs*1e6, 30)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}

		if svcFilter != "" {
			// Subgraph blast-radius filtering
			state = filterSubgraph(state, svcFilter)
		}

		return c.JSON(state)
	})

	// Postmortem incident notes
	api.Get("/notes", func(c *fiber.Ctx) error {
		incidentID := c.Query("incident_id", "default")
		notes, err := meta.GetIncidentNotes(c.Context(), incidentID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(notes)
	})

	api.Post("/notes", func(c *fiber.Ctx) error {
		var note store.IncidentNote
		if err := c.BodyParser(&note); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if note.CreatedAt == 0 {
			note.CreatedAt = time.Now().UnixNano()
		}
		if note.IncidentID == "" {
			note.IncidentID = "default"
		}
		if err := meta.SaveIncidentNote(c.Context(), &note); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusCreated).JSON(note)
	})

	// Bookmarks
	api.Get("/bookmarks", func(c *fiber.Ctx) error {
		bms, err := meta.GetBookmarks(c.Context())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(bms)
	})

	api.Post("/bookmarks", func(c *fiber.Ctx) error {
		var bm store.SavedBookmark
		if err := c.BodyParser(&bm); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if bm.CreatedAt == 0 {
			bm.CreatedAt = time.Now().UnixNano()
		}
		if err := meta.SaveBookmark(c.Context(), &bm); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusCreated).JSON(bm)
	})
}

func parseQueryInt64(c *fiber.Ctx, key string, fallback int64) int64 {
	valStr := c.Query(key)
	if valStr == "" {
		return fallback
	}
	val, err := strconv.ParseInt(valStr, 10, 64)
	if err != nil {
		return fallback
	}
	return val
}

func parseQueryInt(c *fiber.Ctx, key string, fallback int) int {
	valStr := c.Query(key)
	if valStr == "" {
		return fallback
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return fallback
	}
	return val
}

func filterSubgraph(state *types.PointInTimeState, rootService string) *types.PointInTimeState {
	connectedServices := map[string]bool{rootService: true}
	// Find immediate edges
	for _, e := range state.Edges {
		if e.Source == rootService || e.Target == rootService {
			connectedServices[e.Source] = true
			connectedServices[e.Target] = true
		}
	}

	var filteredNodes []types.ServiceNode
	for _, n := range state.Nodes {
		if connectedServices[n.Name] {
			filteredNodes = append(filteredNodes, n)
		}
	}

	var filteredEdges []types.ServiceEdge
	for _, e := range state.Edges {
		if connectedServices[e.Source] && connectedServices[e.Target] {
			filteredEdges = append(filteredEdges, e)
		}
	}

	cloned := *state
	cloned.Nodes = filteredNodes
	cloned.Edges = filteredEdges
	return &cloned
}
