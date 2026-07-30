package importer

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jaecopzm/zedstream/pkg/response"
)

type Handler struct {
	importer *Importer
}

func NewHandler(imp *Importer) *Handler {
	return &Handler{importer: imp}
}

type importTrackRequest struct {
	URL             string  `json:"url"`
	Search          string  `json:"search"`
	GenreID         *string `json:"genre_id"`
	OverrideArtist  string  `json:"override_artist"`
	OverrideTitle   string  `json:"override_title"`
	Description     string  `json:"description"`
}

type importURLRequest struct {
	URL string `json:"url"`
}

func (h *Handler) ImportTrack(w http.ResponseWriter, r *http.Request) {
	var req importTrackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid JSON body")
		return
	}

	opts := ImportOptions{
		GenreID:        req.GenreID,
		OverrideArtist: req.OverrideArtist,
		OverrideTitle:  req.OverrideTitle,
		Description:    req.Description,
	}

	if req.URL != "" {
		if err := h.importer.ImportTrackWithOptions(r.Context(), req.URL, opts); err != nil {
			response.InternalServerError(w, err.Error())
			return
		}
		response.OK(w, map[string]string{"message": "track imported"})
		return
	}

	if req.Search != "" {
		if err := h.importer.ImportSearchWithOptions(r.Context(), req.Search, opts); err != nil {
			response.InternalServerError(w, err.Error())
			return
		}
		response.OK(w, map[string]string{"message": "track imported"})
		return
	}

	response.BadRequest(w, "provide url or search")
}

func (h *Handler) ImportAlbum(w http.ResponseWriter, r *http.Request) {
	var req importURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid JSON body")
		return
	}
	if req.URL == "" {
		response.BadRequest(w, "url is required")
		return
	}
	if err := h.importer.importAlbum(r.Context(), extractSpotifyID(req.URL)); err != nil {
		response.InternalServerError(w, err.Error())
		return
	}
	response.OK(w, map[string]string{"message": "album imported"})
}

func (h *Handler) ImportPlaylist(w http.ResponseWriter, r *http.Request) {
	var req importURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid JSON body")
		return
	}
	if req.URL == "" {
		response.BadRequest(w, "url is required")
		return
	}
	if err := h.importer.importPlaylist(r.Context(), extractSpotifyID(req.URL)); err != nil {
		response.InternalServerError(w, err.Error())
		return
	}
	response.OK(w, map[string]string{"message": "playlist imported"})
}

// ── Search / Preview / Bulk ─────────────────────────────────────────────────

type SearchTracksResponse struct {
	Tracks []SearchResultTrack `json:"tracks"`
}

type SearchResultTrack struct {
	SpotifyID    string   `json:"spotify_id"`
	Title        string   `json:"title"`
	Artists      []string `json:"artists"`
	ArtistIDs    []string `json:"artist_ids"`
	ArtistGenres []string `json:"artist_genres"`
	ISRC         string   `json:"isrc"`
	DurationMs   int      `json:"duration_ms"`
	AlbumName    string   `json:"album_name"`
	CoverURL     string   `json:"cover_url"`
}

func (h *Handler) SearchTracks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		response.BadRequest(w, "query parameter q is required")
		return
	}
	tracks, err := h.importer.spotify.SearchTracks(q, 20)
	if err != nil {
		response.InternalServerError(w, err.Error())
		return
	}
	result := make([]SearchResultTrack, 0, len(tracks))
	for _, t := range tracks {
		artists := make([]string, 0, len(t.Artists))
		artistIDs := make([]string, 0, len(t.Artists))
		for _, a := range t.Artists {
			artists = append(artists, a.Name)
			artistIDs = append(artistIDs, a.ID)
		}
		coverURL := ""
		if len(t.Album.Images) > 0 {
			coverURL = t.Album.Images[0].URL
		}

		// Fetch artist genres from Spotify (primary artist only)
		var artistGenres []string
		if len(t.Artists) > 0 {
			if artist, err := h.importer.spotify.FetchArtist(t.Artists[0].ID); err == nil {
				artistGenres = artist.Genres
			}
		}

		result = append(result, SearchResultTrack{
			SpotifyID:    t.ID,
			Title:        t.Name,
			Artists:      artists,
			ArtistIDs:    artistIDs,
			ArtistGenres: artistGenres,
			ISRC:         t.ExternalIDs.ISRC,
			DurationMs:   t.DurationMs,
			AlbumName:    t.Album.Name,
			CoverURL:     coverURL,
		})
	}
	response.OK(w, SearchTracksResponse{Tracks: result})
}

type BulkImportItem struct {
	SpotifyID       string  `json:"spotify_id"`
	Search          string  `json:"search"`
	OverrideTitle   string  `json:"override_title"`
	OverrideArtist  string  `json:"override_artist"`
	GenreID         *string `json:"genre_id"`
	FeaturedArtists string  `json:"featured_artists"`
	AlbumTitle      string  `json:"album_title"`
	Publish         bool    `json:"publish"`
	Section         string  `json:"section"`
	Description     string  `json:"description"`
}

type BulkImportRequest struct {
	Tracks []BulkImportItem `json:"tracks"`
}

func (h *Handler) BulkImport(w http.ResponseWriter, r *http.Request) {
	var req BulkImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid JSON body")
		return
	}
	if len(req.Tracks) == 0 {
		response.BadRequest(w, "no tracks provided")
		return
	}

	type result struct {
		Index int    `json:"index"`
		Error string `json:"error,omitempty"`
		Title string `json:"title,omitempty"`
		ID    string `json:"id,omitempty"`
	}
	results := make([]result, 0, len(req.Tracks))

	for i, item := range req.Tracks {
		opts := ImportOptions{
			GenreID:         item.GenreID,
			OverrideArtist:  item.OverrideArtist,
			OverrideTitle:   item.OverrideTitle,
			FeaturedArtists: item.FeaturedArtists,
			Publish:         item.Publish,
			Section:         item.Section,
			Description:     item.Description,
		}

		var err error
		if item.SpotifyID != "" {
			err = h.importer.ImportTrackWithOptions(r.Context(), item.SpotifyID, opts)
		} else if item.Search != "" {
			err = h.importer.ImportSearchWithOptions(r.Context(), item.Search, opts)
		} else {
			results = append(results, result{Index: i, Error: "no spotify_id or search provided"})
			continue
		}

		r := result{Index: i}
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Title = item.OverrideTitle
			if r.Title == "" {
				r.Title = item.Search
				if r.Title == "" {
					r.Title = "track " + item.SpotifyID
				}
			}
		}
		results = append(results, r)
	}

	response.OK(w, map[string]any{"results": results})
}

// BulkImportStream streams per-track progress over Server-Sent Events.
// POST /admin/import/bulk/stream
func (h *Handler) BulkImportStream(w http.ResponseWriter, r *http.Request) {
	var req BulkImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid JSON body")
		return
	}
	if len(req.Tracks) == 0 {
		response.BadRequest(w, "no tracks provided")
		return
	}

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	type trackEvent struct {
		Type  string `json:"type"` // "progress" | "done" | "error"
		Index int    `json:"index"`
		Total int    `json:"total"`
		Title string `json:"title,omitempty"`
		Error string `json:"error,omitempty"`
	}

	send := func(evt trackEvent) {
		b, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	ctx := r.Context()
	total := len(req.Tracks)

	for i, item := range req.Tracks {
		// Bail early if client disconnected
		select {
		case <-ctx.Done():
			return
		default:
		}

		title := item.OverrideTitle
		if title == "" {
			title = item.Search
			if title == "" {
				title = "track " + item.SpotifyID
			}
		}

		opts := ImportOptions{
			GenreID:         item.GenreID,
			OverrideArtist:  item.OverrideArtist,
			OverrideTitle:   item.OverrideTitle,
			FeaturedArtists: item.FeaturedArtists,
			Publish:         item.Publish,
			Section:         item.Section,
			Description:     item.Description,
		}

		var err error
		if item.SpotifyID != "" {
			err = h.importer.ImportTrackWithOptions(ctx, item.SpotifyID, opts)
		} else if item.Search != "" {
			err = h.importer.ImportSearchWithOptions(ctx, item.Search, opts)
		} else {
			send(trackEvent{Type: "error", Index: i, Total: total, Title: title, Error: "no spotify_id or search provided"})
			continue
		}

		if err != nil {
			send(trackEvent{Type: "error", Index: i, Total: total, Title: title, Error: err.Error()})
		} else {
			send(trackEvent{Type: "progress", Index: i, Total: total, Title: title})
		}
	}

	// Final done event
	send(trackEvent{Type: "done", Index: total - 1, Total: total})
}

func extractSpotifyID(rawURL string) string {
	m := spotifyURLRegexp.FindStringSubmatch(rawURL)
	if len(m) < 3 {
		return rawURL
	}
	return m[2]
}
