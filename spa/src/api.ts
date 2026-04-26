// Thin fetch wrappers around /api/v1/*. All return parsed JSON or throw.

import type { Creator, Group, GroupFeed, Video } from './types'

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
    groupFeed: (slug: string, opts?: { unseen?: boolean; tag?: string }) => {
        const params = new URLSearchParams()
        if (opts?.unseen) params.set('unseen', '1')
        if (opts?.tag) params.set('tag', opts.tag)
        const qs = params.toString()
        return req<GroupFeed>(`/api/v1/groups/${slug}/feed${qs ? '?' + qs : ''}`)
    },
}

export { ApiError }
