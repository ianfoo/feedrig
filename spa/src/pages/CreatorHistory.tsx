import { useParams, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import VideoCard from '../components/VideoCard'

export default function CreatorHistory() {
    const { id } = useParams<{ id: string }>()
    const cid = Number(id)
    const qc = useQueryClient()

    const { data: creators } = useQuery({ queryKey: ['creators'], queryFn: api.listCreators })
    const c = creators?.find((c) => c.id === cid)

    const { data: videos, isLoading, error } = useQuery({
        queryKey: ['creator-videos-all', cid],
        queryFn: () => api.creatorVideos(cid, true),
    })

    const redownload = useMutation({
        mutationFn: (vid: number) => api.redownloadVideo(vid),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: ['creator-videos-all', cid] })
            qc.invalidateQueries({ queryKey: ['creator-videos', cid] })
        },
    })

    if (isLoading) return <div className="panel">Loading…</div>
    if (error) return <div className="panel text-danger">Failed: {(error as Error).message}</div>

    const items = videos ?? []

    return (
        <div>
            <section className="panel">
                <div className="flex justify-between items-start gap-3 flex-wrap">
                    <div>
                        <h1 className="text-xl font-semibold">@{c?.handle ?? id} · history</h1>
                        <p className="text-fgdim text-sm mt-1">Every video ever fetched, including ones whose media has been swept. Re-download to bring an archived video back.</p>
                    </div>
                    <Link to={`/creators/${cid}`} className="btn">← Active</Link>
                </div>
            </section>

            <section className="panel">
                {items.length === 0 ? (
                    <p className="text-fgdim">No history yet.</p>
                ) : (
                    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                        {items.map((v) => (
                            <div key={v.id} className="bg-panel2 rounded-xl overflow-hidden border border-border">
                                <VideoCard video={v} creator={c?.handle} showState />
                                {v.state === 'archived' && (
                                    <div className="px-2.5 pb-2.5">
                                        <button
                                            className="btn btn-primary text-xs w-full"
                                            onClick={() => redownload.mutate(v.id)}
                                            disabled={redownload.isPending}
                                        >Re-download</button>
                                    </div>
                                )}
                            </div>
                        ))}
                    </div>
                )}
            </section>
        </div>
    )
}
