import { Link } from 'react-router-dom'
import type { Video } from '../types'

interface Props {
    video: Video
    creator?: string
    showState?: boolean
}

export default function VideoCard({ video, creator, showState }: Props) {
    return (
        <Link to={`/videos/${video.id}`} className="group block bg-panel2 rounded-xl overflow-hidden border border-transparent hover:border-border hover:-translate-y-px transition-all no-underline">
            <div className="aspect-square bg-black">
                {video.thumbnail_url ? (
                    <img src={video.thumbnail_url} alt="" className="w-full h-full object-cover" loading="lazy" />
                ) : (
                    <div className="w-full h-full bg-gradient-to-br from-panel to-panel2" />
                )}
            </div>
            <div className="p-2.5">
                <div className="font-semibold text-sm leading-tight line-clamp-2 text-fg">
                    {video.title || '(untitled)'}
                </div>
                <div className="text-xs text-fgdim mt-1">
                    {creator && <span>@{creator} · </span>}
                    {fmtTime(video.posted_at ?? video.downloaded_at)}
                    {video.duration_seconds ? ' · ' + fmtDuration(video.duration_seconds) : ''}
                </div>
                {video.tags && video.tags.length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-1.5">
                        {video.tags.map((t) => <span key={t} className="tag">{t}</span>)}
                    </div>
                )}
                {video.summary && (
                    <p className="text-fgdim text-sm mt-1.5 line-clamp-3 leading-snug">{video.summary}</p>
                )}
                {showState && video.state !== 'active' && (
                    <span className={`badge mt-1.5 ${stateBadgeClass(video.state)}`}>{video.state}</span>
                )}
            </div>
        </Link>
    )
}

function stateBadgeClass(state: Video['state']) {
    if (state === 'saved') return 'badge-saved'
    if (state === 'archived') return 'badge-archived'
    if (state === 'pending_deletion') return 'badge-pending'
    return ''
}

function fmtTime(unix: number) {
    return new Date(unix * 1000).toLocaleString(undefined, {
        month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
    })
}

function fmtDuration(s: number) {
    const m = Math.floor(s / 60)
    const r = s % 60
    return `${m}:${r.toString().padStart(2, '0')}`
}
