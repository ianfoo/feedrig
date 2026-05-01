// Mirrors the Go DTOs in internal/web/api.go. Keep these in sync when the
// API changes. (A future build step could codegen these from Go; for now,
// hand-maintained is fine for the surface we have.)

export type IngestMode = 'full' | 'preview'

export interface Creator {
    id: number
    handle: string
    display_name?: string
    profile_url: string
    added_at: number
    last_fetched_at?: number
    top_tags?: string[]
    ingest_mode?: IngestMode
    ttl_days_override?: number
}

export type VideoState = 'active' | 'saved' | 'pending_deletion' | 'archived' | 'preview'

export interface Video {
    id: number
    creator_id: number
    external_id: string
    url: string
    title?: string
    description?: string
    duration_seconds?: number
    posted_at?: number
    downloaded_at: number
    state: VideoState
    media_url: string
    thumbnail_url?: string
    tags?: string[]
    summary?: string
    transcript?: string
}

export interface Group {
    id: number
    slug: string
    name: string
    recency_days: number
    include_tags?: string[]
    exclude_tags?: string[]
    last_visited_at?: number
}

export interface GroupFeed {
    group: Group
    videos: Video[]
}
