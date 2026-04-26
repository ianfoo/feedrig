// Package mcp speaks the Model Context Protocol over stdio so an LLM (e.g.
// Claude Desktop, Cline) can search and inspect feedrig's corpus directly.
//
// This is a deliberately small JSON-RPC 2.0 implementation — protocol shell
// only — that delegates everything substantive to the existing internal/
// stores. The protocol surface today:
//
//   initialize             → server capabilities + tools
//   tools/list             → list of tool definitions
//   tools/call             → invoke a tool by name
//   prompts/list           → empty (we expose tools, not prompts)
//   resources/list         → empty
//   notifications/initialized → ack, no response
//
// Tools exposed:
//
//   search_videos(query, limit?)
//   get_video(id)
//   list_creators()
//   list_groups()
//
// Wire from a client: `feedrig mcp -data <path-to-data-dir>`.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/enrich"
	"github.com/ianfoo/feedrig/internal/groups"
	"github.com/ianfoo/feedrig/internal/video"
)

// Protocol version we report back. The MCP spec evolves; this string is what
// most current clients accept.
const protocolVersion = "2024-11-05"

type Server struct {
	Creators *creator.Store
	Videos   *video.Store
	Enrich   *enrich.Store
	Groups   *groups.Store
	Log      *slog.Logger
}

func (s *Server) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Serve reads JSON-RPC messages line-delimited from in, writes responses to
// out. Returns when in is closed.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.write(enc, errorResponse(nil, -32700, "parse error", err.Error()))
			continue
		}
		resp := s.dispatch(ctx, req)
		if resp != nil {
			s.write(enc, resp)
		}
	}
	return scanner.Err()
}

func (s *Server) write(enc *json.Encoder, resp *rpcResponse) {
	if err := enc.Encode(resp); err != nil {
		s.log().Warn("mcp encode", "err", err)
	}
}

// dispatch returns nil for notifications (no response wanted).
func (s *Server) dispatch(ctx context.Context, req rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return okResponse(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"serverInfo":      map[string]string{"name": "feedrig", "version": "0.6"},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})
	case "notifications/initialized":
		return nil // ack-only, no response
	case "tools/list":
		return okResponse(req.ID, map[string]any{"tools": toolDefs})
	case "tools/call":
		return s.handleToolCall(ctx, req)
	case "prompts/list":
		return okResponse(req.ID, map[string]any{"prompts": []any{}})
	case "resources/list":
		return okResponse(req.ID, map[string]any{"resources": []any{}})
	case "ping":
		return okResponse(req.ID, map[string]any{})
	default:
		return errorResponse(req.ID, -32601, "method not found", req.Method)
	}
}

// JSON-RPC types -----------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func okResponse(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, msg string, data any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg, Data: data}}
}

// Tool definitions ---------------------------------------------------

var toolDefs = []map[string]any{
	{
		"name":        "search_videos",
		"description": "Search the local video corpus across title, description, summary, and transcript. Returns up to `limit` matching videos with a short excerpt around the match.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Substring to search for (case-insensitive)."},
				"limit": map[string]any{"type": "integer", "description": "Maximum results, default 50.", "minimum": 1},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "get_video",
		"description": "Fetch full details for a single video, including the transcript and summary if available.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "integer", "description": "Video id (from search_videos results)."},
			},
			"required": []string{"id"},
		},
	},
	{
		"name":        "list_creators",
		"description": "List the curated creators with their display names, handles, and top tags (most-frequent topics across their videos).",
		"inputSchema": map[string]any{"type": "object"},
	},
	{
		"name":        "list_groups",
		"description": "List the smart-playlist groups with recency window and tag filters.",
		"inputSchema": map[string]any{"type": "object"},
	},
}

// Tool dispatch ------------------------------------------------------

func (s *Server) handleToolCall(ctx context.Context, req rpcRequest) *rpcResponse {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResponse(req.ID, -32602, "invalid params", err.Error())
	}
	switch p.Name {
	case "search_videos":
		return s.toolSearchVideos(ctx, req.ID, p.Arguments)
	case "get_video":
		return s.toolGetVideo(ctx, req.ID, p.Arguments)
	case "list_creators":
		return s.toolListCreators(ctx, req.ID)
	case "list_groups":
		return s.toolListGroups(ctx, req.ID)
	default:
		return errorResponse(req.ID, -32601, "unknown tool", p.Name)
	}
}

func (s *Server) toolSearchVideos(ctx context.Context, id, raw json.RawMessage) *rpcResponse {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResponse(id, -32602, "invalid arguments", err.Error())
	}
	hits, err := s.Enrich.Search(ctx, args.Query, args.Limit)
	if err != nil {
		return errorResponse(id, -32603, "search failed", err.Error())
	}

	type out struct {
		ID      int64  `json:"id"`
		Title   string `json:"title"`
		Creator string `json:"creator"`
		Field   string `json:"matched_in"`
		Excerpt string `json:"excerpt"`
	}
	rows := make([]out, 0, len(hits))
	creatorCache := map[int64]string{}
	for _, h := range hits {
		handle := creatorCache[h.CreatorID]
		if handle == "" {
			if c, err := s.Creators.Get(ctx, h.CreatorID); err == nil {
				handle = c.Handle
				creatorCache[h.CreatorID] = handle
			}
		}
		rows = append(rows, out{ID: h.VideoID, Title: h.Title, Creator: handle, Field: h.Field, Excerpt: h.Excerpt})
	}
	return okResponse(id, contentEnvelope(rows))
}

func (s *Server) toolGetVideo(ctx context.Context, id, raw json.RawMessage) *rpcResponse {
	var args struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResponse(id, -32602, "invalid arguments", err.Error())
	}
	v, err := s.Videos.Get(ctx, args.ID)
	if err != nil {
		return errorResponse(id, -32603, "video lookup failed", err.Error())
	}
	c, _ := s.Creators.Get(ctx, v.CreatorID)
	summary, _ := s.Enrich.GetSummary(ctx, v.ID)
	transcript, _ := s.Enrich.GetTranscript(ctx, v.ID)
	tags, _ := s.Enrich.TagsForVideo(ctx, v.ID)
	tagNames := make([]string, len(tags))
	for i, t := range tags {
		tagNames[i] = t.Name
	}
	out := map[string]any{
		"id":          v.ID,
		"title":       v.Title,
		"description": v.Description,
		"url":         v.URL,
		"state":       string(v.State),
		"creator":     "",
		"posted_at":   nil,
		"tags":        tagNames,
	}
	if c != nil {
		out["creator"] = c.Handle
	}
	if v.PostedAt != nil {
		out["posted_at"] = v.PostedAt.Unix()
	}
	if summary != nil {
		out["summary"] = summary.Summary
		if summary.Notes != "" {
			out["notes"] = summary.Notes
		}
	}
	if transcript != nil {
		out["transcript"] = transcript.Text
	}
	return okResponse(id, contentEnvelope(out))
}

func (s *Server) toolListCreators(ctx context.Context, id json.RawMessage) *rpcResponse {
	cs, err := s.Creators.List(ctx, creator.SortHandle)
	if err != nil {
		return errorResponse(id, -32603, "list failed", err.Error())
	}
	type out struct {
		ID          int64    `json:"id"`
		Handle      string   `json:"handle"`
		DisplayName string   `json:"display_name,omitempty"`
		TopTags     []string `json:"top_tags,omitempty"`
	}
	rows := make([]out, len(cs))
	for i, c := range cs {
		row := out{ID: c.ID, Handle: c.Handle, DisplayName: c.DisplayName}
		if tcs, _ := s.Enrich.TopTagsForCreator(ctx, c.ID, 5); len(tcs) > 0 {
			row.TopTags = make([]string, len(tcs))
			for j, t := range tcs {
				row.TopTags[j] = t.Name
			}
		}
		rows[i] = row
	}
	return okResponse(id, contentEnvelope(rows))
}

func (s *Server) toolListGroups(ctx context.Context, id json.RawMessage) *rpcResponse {
	gs, err := s.Groups.List(ctx)
	if err != nil {
		return errorResponse(id, -32603, "list failed", err.Error())
	}
	type out struct {
		Slug        string   `json:"slug"`
		Name        string   `json:"name"`
		RecencyDays int      `json:"recency_days"`
		IncludeTags []string `json:"include_tags,omitempty"`
		ExcludeTags []string `json:"exclude_tags,omitempty"`
	}
	rows := make([]out, len(gs))
	for i, g := range gs {
		rows[i] = out{Slug: g.Slug, Name: g.Name, RecencyDays: g.RecencyDays, IncludeTags: g.IncludeTags, ExcludeTags: g.ExcludeTags}
	}
	return okResponse(id, contentEnvelope(rows))
}

// contentEnvelope wraps a tool result in the MCP "content" array shape that
// most clients expect. We serialize the payload to JSON and stuff it into a
// text content block — clients render it sensibly, and Claude's tool-use
// flow can re-parse it cleanly.
func contentEnvelope(payload any) map[string]any {
	b, _ := json.Marshal(payload)
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(b)},
		},
		"isError": false,
	}
}

