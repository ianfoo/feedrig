// Thin fetch wrappers around /api/v1/*. All return parsed JSON or throw.

import type { Creator, Group, GroupFeed, Video } from './types'

export interface Stats {
    media_bytes: number
    media_bytes_human: string
    video_count: number
    saved_count: number
    archived_count: number
    pending_count: number
}

export interface Settings {
    poll_hours: number
    ttl_days: number
    grace_days: number
    metadata_ttl_days: number
}

export interface GroupWithMembers {
    group: Group
    members: { creator_id: number; excluded: boolean }[]
}

class ApiError extends Error {
    constructor(public status: number, message: string) {
        super(message)
    }
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
    const r = await fetch(path, {
        ...init,
        headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
    })
    if (!r.ok) {
        const text = await r.text().catch(() => '')
        throw new ApiError(r.status, text || r.statusText)
    }
    if (r.status === 204) return undefined as T
    return r.json() as Promise<T>
}

export const api = {
    listCreators: () => req<Creator[]>('/api/v1/creators'),
    addCreator: (handle: string, display_name?: string) =>
        req<Creator>('/api/v1/creators', { method: 'POST', body: JSON.stringify({ handle, display_name }) }),
    deleteCreator: (id: number) =>
        req<void>(`/api/v1/creators/${id}`, { method: 'DELETE' }),

    getVideo: (id: number) => req<Video>(`/api/v1/videos/${id}`),
    saveVideo: (id: number) => req<void>(`/api/v1/videos/${id}/save`, { method: 'POST' }),
    deleteVideo: (id: number) => req<void>(`/api/v1/videos/${id}/delete`, { method: 'POST' }),
    savePosition: (id: number, position: number, watched: boolean) => {
        const body = new URLSearchParams({ position: String(position), watched: watched ? '1' : '0' })
        // Use sendBeacon when available so navigation away doesn't drop the
        // request. Fallback fetch keeps things working in tests and dev.
        if (typeof navigator !== 'undefined' && 'sendBeacon' in navigator) {
            navigator.sendBeacon(`/api/v1/videos/${id}/position`, body)
            return
        }
        return fetch(`/api/v1/videos/${id}/position`, { method: 'POST', body, keepalive: true })
    },

    listGroups: () => req<Group[]>('/api/v1/groups'),
    getGroup: (slug: string) => req<GroupWithMembers>(`/api/v1/groups/${slug}`),
    createGroup: (body: { name: string; recency_days: number; include_tags?: string[]; exclude_tags?: string[] }) =>
        req<Group>('/api/v1/groups', { method: 'POST', body: JSON.stringify(body) }),
    updateGroup: (slug: string, body: { name: string; recency_days: number; include_tags?: string[]; exclude_tags?: string[] }) =>
        req<void>(`/api/v1/groups/${slug}`, { method: 'PUT', body: JSON.stringify(body) }),
    deleteGroup: (slug: string) =>
        req<void>(`/api/v1/groups/${slug}`, { method: 'DELETE' }),
    setGroupMembers: (slug: string, include: number[], exclude: number[]) =>
        req<void>(`/api/v1/groups/${slug}/members`, { method: 'POST', body: JSON.stringify({ include, exclude }) }),
    reorderGroup: (slug: string, dir: 'up' | 'down') =>
        req<void>(`/api/v1/groups/${slug}/reorder`, { method: 'POST', body: JSON.stringify({ dir }) }),
    groupFeed: (slug: string, opts?: { unseen?: boolean; tag?: string }) => {
        const params = new URLSearchParams()
        if (opts?.unseen) params.set('unseen', '1')
        if (opts?.tag) params.set('tag', opts.tag)
        const qs = params.toString()
        return req<GroupFeed>(`/api/v1/groups/${slug}/feed${qs ? '?' + qs : ''}`)
    },

    creatorVideos: (id: number, includeArchived = false) =>
        req<Video[]>(`/api/v1/creators/${id}/videos${includeArchived ? '?include_archived=1' : ''}`),
    fetchCreator: (id: number) =>
        req<{ started: boolean; creator: string }>(`/api/v1/creators/${id}/fetch`, { method: 'POST' }),
    fetchAll: () =>
        req<{ started: number; skipped: number }>('/api/v1/creators/fetch-all', { method: 'POST' }),
    setCadence: (id: number, pollHours: number) =>
        req<void>(`/api/v1/creators/${id}/cadence`, { method: 'POST', body: JSON.stringify({ poll_hours: pollHours }) }),
    bulkAddCreators: (list: string) =>
        req<{ added: number; skipped: number; failed: number }>('/api/v1/creators/bulk', { method: 'POST', body: JSON.stringify({ list }) }),

    listPending: () => req<Video[]>('/api/v1/pending'),
    restoreVideo: (id: number) =>
        req<void>(`/api/v1/videos/${id}/restore`, { method: 'POST' }),
    redownloadVideo: (id: number) =>
        req<void>(`/api/v1/videos/${id}/redownload`, { method: 'POST' }),

    getSettings: () => req<Settings>('/api/v1/settings'),
    putSettings: (body: Partial<Settings>) =>
        req<void>('/api/v1/settings', { method: 'PUT', body: JSON.stringify(body) }),

    getStats: () => req<Stats>('/api/v1/stats'),
}

export { ApiError }
