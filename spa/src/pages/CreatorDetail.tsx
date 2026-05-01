import { useParams, Link, useSearchParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useEffect } from 'react'
import { api } from '../api'
import VideoCard from '../components/VideoCard'
import ModePicker from '../components/ModePicker'
import type { IngestMode } from '../types'

export default function CreatorDetail() {
    const { id } = useParams<{ id: string }>()
    const cid = Number(id)
    const qc = useQueryClient()
    const [params, setParams] = useSearchParams()
    const activeTag = (params.get('tag') ?? '').toLowerCase()

    const { data: creators } = useQuery({ queryKey: ['creators'], queryFn: api.listCreators })
    const c = creators?.find((c) => c.id === cid)

    const { data: videos, isLoading: vidLoading, error: vidError } = useQuery({
        queryKey: ['creator-videos', cid],
        queryFn: () => api.creatorVideos(cid),
    })

    const fetchMutation = useMutation({
        mutationFn: () => api.fetchCreator(cid),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: ['creator-videos', cid] })
            qc.invalidateQueries({ queryKey: ['creators'] })
        },
    })

    const [pollHours, setPollHours] = useState<string>('')
    useEffect(() => {
        if (c?.last_fetched_at !== undefined) {
            // Server doesn't currently expose poll_interval_seconds in the
            // creator DTO; this field is best-effort visible via the legacy
            // page. Future API addition.
        }
    }, [c])

    const cadenceMutation = useMutation({
        mutationFn: () => api.setCadence(cid, pollHours === '' ? 0 : parseInt(pollHours, 10)),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['creators'] }),
    })

    const modeMutation = useMutation({
        mutationFn: (m: IngestMode) => api.setCreatorMode(cid, m),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['creators'] }),
    })

    const [ttlOverride, setTTLOverride] = useState<string>('')
    useEffect(() => {
        if (c?.ttl_days_override !== undefined) {
            setTTLOverride(c.ttl_days_override > 0 ? String(c.ttl_days_override) : '')
        }
    }, [c?.ttl_days_override])

    const ttlMutation = useMutation({
        mutationFn: () => api.setCreatorTTL(cid, ttlOverride === '' ? 0 : parseInt(ttlOverride, 10)),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['creators'] }),
    })

    // Compute top tags from the creator's videos client-side. Cheap at
    // personal-use scale and avoids a dedicated API endpoint.
    const topTags = (() => {
        const counts = new Map<string, number>()
        for (const v of videos ?? []) {
            for (const t of v.tags ?? []) {
                counts.set(t, (counts.get(t) ?? 0) + 1)
            }
        }
        return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, 8)
    })()

    const filtered = (videos ?? []).filter((v) =>
        activeTag === '' || (v.tags ?? []).map((t) => t.toLowerCase()).includes(activeTag)
    )

    if (vidError) return <div className="panel text-danger">Failed: {(vidError as Error).message}</div>

    return (
        <div>
            <section className="panel">
                <div className="flex justify-between items-start gap-3 flex-wrap">
                    <div>
                        <h1 className="text-xl font-semibold">@{c?.handle ?? id}</h1>
                        {c?.display_name && <p className="text-fgdim text-sm">{c.display_name}</p>}
                        <p className="text-fgdim text-xs mt-1">
                            {c?.last_fetched_at ? `Last fetched ${new Date(c.last_fetched_at * 1000).toLocaleString()}` : 'Never fetched'}
                        </p>
                        {topTags.length > 0 && (
                            <div className="flex flex-wrap gap-1 items-baseline mt-2 text-sm">
                                <span className="text-fgdim">Topics:</span>
                                {topTags.map(([name, count]) => (
                                    <button
                                        key={name}
                                        className={`tag ${name.toLowerCase() === activeTag ? 'bg-accent text-[#0b1220] border-accent' : 'tag-link'}`}
                                        onClick={() => {
                                            const next = new URLSearchParams(params)
                                            if (name.toLowerCase() === activeTag) next.delete('tag')
                                            else next.set('tag', name)
                                            setParams(next, { replace: true })
                                        }}
                                    >
                                        {name} <span className="ml-0.5 text-xs opacity-70">{count}</span>
                                    </button>
                                ))}
                                {activeTag && (
                                    <button
                                        className="text-xs text-fgdim ml-1 underline"
                                        onClick={() => {
                                            const next = new URLSearchParams(params)
                                            next.delete('tag')
                                            setParams(next, { replace: true })
                                        }}
                                    >clear</button>
                                )}
                            </div>
                        )}
                    </div>
                    <div className="flex gap-2 items-center">
                        <Link to={`/creators/${cid}/history`} className="btn">History</Link>
                        <button
                            className="btn btn-primary"
                            onClick={() => fetchMutation.mutate()}
                            disabled={fetchMutation.isPending}
                        >Fetch new</button>
                    </div>
                </div>

                {fetchMutation.isSuccess && (
                    <p className="text-fgdim text-sm mt-3">
                        {fetchMutation.data?.started ? 'Fetch started in the background — refresh in a minute.' : `Already fetching @${fetchMutation.data?.creator}.`}
                    </p>
                )}
            </section>

            <section className="panel">
                <details>
                    <summary className="text-fgdim text-sm cursor-pointer">Per-creator settings</summary>
                    <div className="mt-3 space-y-4">
                        <div>
                            <label className="block text-fgdim text-xs">Ingest mode</label>
                            {c && (
                                <ModePicker
                                    label=""
                                    value={(c.ingest_mode ?? 'full') as IngestMode}
                                    onChange={(m) => modeMutation.mutate(m)}
                                />
                            )}
                            <p className="text-fgdim text-xs mt-1">Full = download videos. Preview = caption + thumbnail only; download on demand from a card.</p>
                        </div>

                        <div>
                            <label className="block text-fgdim text-xs">Polling cadence override (hours)</label>
                            <form className="flex gap-2 mt-1" onSubmit={(e) => { e.preventDefault(); cadenceMutation.mutate() }}>
                                <input type="number" min={0} max={168} placeholder="hours (empty = use default)" className="input max-w-[14rem]" value={pollHours} onChange={(e) => setPollHours(e.target.value)} />
                                <button type="submit" className="btn">Save</button>
                            </form>
                        </div>

                        <div>
                            <label className="block text-fgdim text-xs">TTL override (days)</label>
                            <form className="flex gap-2 mt-1" onSubmit={(e) => { e.preventDefault(); ttlMutation.mutate() }}>
                                <input type="number" min={0} max={3650} placeholder="days (empty = use global)" className="input max-w-[14rem]" value={ttlOverride} onChange={(e) => setTTLOverride(e.target.value)} />
                                <button type="submit" className="btn">Save</button>
                            </form>
                            <p className="text-fgdim text-xs mt-1">Shorter for noisy creators (e.g. 3 days), longer for ones you save from often. Empty / 0 inherits the global TTL.</p>
                        </div>
                    </div>
                </details>
            </section>

            <section className="panel">
                {vidLoading && <p className="text-fgdim">Loading…</p>}
                {!vidLoading && filtered.length === 0 && (
                    <p className="text-fgdim">
                        {activeTag ? <>No videos tagged <span className="font-semibold">{activeTag}</span>. <button className="underline" onClick={() => { const n = new URLSearchParams(params); n.delete('tag'); setParams(n, { replace: true }) }}>Show all</button>.</> : 'No videos yet — click Fetch new.'}
                    </p>
                )}
                <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                    {filtered.map((v) => (
                        <VideoCard key={v.id} video={v} creator={c?.handle} showState />
                    ))}
                </div>
            </section>

            <section className="panel">
                <p className="text-fgdim text-sm"><Link to="/creators">← back to creators</Link></p>
            </section>
        </div>
    )
}
