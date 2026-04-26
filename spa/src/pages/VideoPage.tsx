import { useParams, useNavigate, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { api } from '../api'

const SPEED_PRESETS = [0.75, 1.0, 1.25, 1.5, 1.75, 2.0, 2.5]

export default function VideoPage() {
    const { id } = useParams<{ id: string }>()
    const videoID = Number(id)
    const navigate = useNavigate()
    const qc = useQueryClient()

    const { data, isLoading, error } = useQuery({
        queryKey: ['video', videoID],
        queryFn: () => api.getVideo(videoID),
    })

    const saveMutation = useMutation({
        mutationFn: () => api.saveVideo(videoID),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['video', videoID] }),
    })
    const deleteMutation = useMutation({
        mutationFn: () => api.deleteVideo(videoID),
        onSuccess: () => navigate(-1),
    })

    const videoRef = useRef<HTMLVideoElement>(null)
    const [speed, setSpeed] = useState(1)
    const lastSentRef = useRef(0)

    // Throttled position persistence: every 4s + on pause/end + on unload.
    useEffect(() => {
        const v = videoRef.current
        if (!v) return
        const onTime = () => {
            const now = Date.now()
            if (now - lastSentRef.current > 4000) {
                lastSentRef.current = now
                api.savePosition(videoID, v.currentTime, false)
            }
        }
        const onPause = () => api.savePosition(videoID, v.currentTime, false)
        const onEnded = () => api.savePosition(videoID, v.currentTime, true)
        const onUnload = () => {
            const watched = v.duration > 0 && v.currentTime / v.duration > 0.9
            api.savePosition(videoID, v.currentTime, watched)
        }
        v.addEventListener('timeupdate', onTime)
        v.addEventListener('pause', onPause)
        v.addEventListener('ended', onEnded)
        window.addEventListener('beforeunload', onUnload)
        return () => {
            v.removeEventListener('timeupdate', onTime)
            v.removeEventListener('pause', onPause)
            v.removeEventListener('ended', onEnded)
            window.removeEventListener('beforeunload', onUnload)
        }
    }, [videoID])

    // Keyboard: Space play/pause, J/L seek 5s, K toggle, 1–7 speed presets.
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            const target = e.target as HTMLElement
            if (target.matches('input, textarea, select')) return
            const v = videoRef.current
            if (!v) return
            switch (e.key) {
                case ' ':
                    e.preventDefault()
                    v.paused ? v.play() : v.pause()
                    break
                case 'j': case 'J':
                    e.preventDefault()
                    v.currentTime = Math.max(0, v.currentTime - 5)
                    break
                case 'l': case 'L':
                    e.preventDefault()
                    v.currentTime = Math.min(v.duration || 0, v.currentTime + 5)
                    break
                case 'k': case 'K':
                    e.preventDefault()
                    v.paused ? v.play() : v.pause()
                    break
                default:
                    if (e.key >= '1' && e.key <= '7') {
                        const idx = parseInt(e.key, 10) - 1
                        if (idx < SPEED_PRESETS.length) {
                            v.playbackRate = SPEED_PRESETS[idx]
                            setSpeed(SPEED_PRESETS[idx])
                        }
                    }
            }
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [])

    if (isLoading) return <div className="panel">Loading…</div>
    if (error) return <div className="panel text-danger">Failed: {(error as Error).message}</div>
    if (!data) return null
    const v = data

    return (
        <div className="space-y-3">
            {v.state === 'archived' ? (
                <ArchivedShell video={v} />
            ) : (
                <video
                    ref={videoRef}
                    src={v.media_url}
                    poster={v.thumbnail_url}
                    controls
                    preload="metadata"
                    className="w-full max-h-[75vh] bg-black rounded-xl"
                />
            )}

            <section className="panel space-y-2">
                <div className="flex items-center gap-1.5 flex-wrap">
                    <span className="text-fgdim text-sm mr-1">Speed</span>
                    {SPEED_PRESETS.map((s) => (
                        <button
                            key={s}
                            className={`btn ${speed === s ? 'btn-primary' : ''}`}
                            onClick={() => {
                                const vv = videoRef.current
                                if (vv) vv.playbackRate = s
                                setSpeed(s)
                            }}
                        >{s}×</button>
                    ))}
                </div>

                <div className="flex items-center gap-1.5 flex-wrap">
                    <button
                        className="btn btn-primary"
                        onClick={() => saveMutation.mutate()}
                        disabled={saveMutation.isPending || v.state === 'saved'}
                    >Save</button>
                    <button
                        className="btn btn-danger"
                        onClick={() => { if (confirm('Move to pending deletion?')) deleteMutation.mutate() }}
                        disabled={deleteMutation.isPending}
                    >Delete</button>
                    {v.state !== 'active' && <span className={`badge ${badgeClass(v.state)}`}>{v.state}</span>}
                </div>
            </section>

            <section className="panel">
                <h2 className="text-lg font-semibold">{v.title || '(untitled)'}</h2>
                <p className="text-fgdim text-sm mt-1">
                    <a href={v.url} target="_blank" rel="noopener" className="text-accent">on instagram</a>
                    {v.posted_at && <> · posted {new Date(v.posted_at * 1000).toLocaleString()}</>}
                </p>
                {v.tags && v.tags.length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-2">
                        {v.tags.map((t) => <span key={t} className="tag">{t}</span>)}
                    </div>
                )}
                {v.summary && (
                    <div className="mt-3 bg-panel2 border-l-4 border-accent rounded-md p-3">
                        <h3 className="text-xs font-semibold text-fgdim uppercase tracking-wider">Summary</h3>
                        <p className="mt-1">{v.summary}</p>
                    </div>
                )}
                {v.description && (
                    <details className="mt-3">
                        <summary className="text-fgdim text-sm cursor-pointer">Original caption</summary>
                        <p className="whitespace-pre-wrap mt-2 text-sm">{v.description}</p>
                    </details>
                )}
                <p className="text-fgdim text-xs mt-3">
                    Keyboard: <kbd className="bg-panel2 border border-border rounded px-1">Space</kbd> play/pause ·
                    <kbd className="bg-panel2 border border-border rounded px-1 ml-1">J</kbd>/<kbd className="bg-panel2 border border-border rounded px-1">L</kbd> seek 5s ·
                    <kbd className="bg-panel2 border border-border rounded px-1 ml-1">1</kbd>–<kbd className="bg-panel2 border border-border rounded px-1">7</kbd> speed
                </p>
                <p className="text-fgdim text-xs mt-2">
                    <Link to={`/creators/${v.creator_id}`}>← back to creator</Link>
                </p>
            </section>
        </div>
    )
}

function badgeClass(state: string) {
    if (state === 'saved') return 'badge-saved'
    if (state === 'archived') return 'badge-archived'
    if (state === 'pending_deletion') return 'badge-pending'
    return ''
}

function ArchivedShell({ video }: { video: { thumbnail_url?: string; id: number } }) {
    return (
        <div className="relative aspect-video rounded-xl overflow-hidden bg-black">
            {video.thumbnail_url && <img src={video.thumbnail_url} alt="" className="absolute inset-0 w-full h-full object-cover opacity-40" />}
            <div className="relative h-full flex flex-col items-center justify-center gap-2 text-center px-6">
                <h3 className="text-warn font-semibold">Archived</h3>
                <p className="text-fg max-w-md">The media file was removed by the TTL sweeper. Metadata, summary, and transcript are preserved.</p>
                <form method="post" action={`/videos/${video.id}/redownload`}>
                    <button type="submit" className="btn btn-primary">Re-download</button>
                </form>
            </div>
        </div>
    )
}
