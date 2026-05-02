import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../api'

export default function Pending() {
    const qc = useQueryClient()
    const { data, isLoading, error } = useQuery({
        queryKey: ['pending'],
        queryFn: api.listPending,
    })
    const { data: creators } = useQuery({ queryKey: ['creators'], queryFn: api.listCreators })
    const creatorByID = new Map((creators ?? []).map((c) => [c.id, c.handle]))

    const keep = useMutation({
        mutationFn: (id: number) => api.saveVideo(id),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['pending'] }),
    })
    const restore = useMutation({
        mutationFn: (id: number) => api.restoreVideo(id),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['pending'] }),
    })

    if (isLoading) return <div className="panel">Loading…</div>
    if (error) return <div className="panel text-danger">Failed: {(error as Error).message}</div>

    const items = data ?? []

    return (
        <div>
            <section className="panel">
                <h1 className="text-xl font-semibold">Pending deletion</h1>
                <p className="text-fgdim text-sm mt-1">Videos auto-expire after the configured TTL (see <Link to="/settings">Settings</Link>). Once moved here, they're permanently removed after the grace window unless you Keep or Restore them.</p>
            </section>

            {items.length === 0 ? (
                <section className="panel">
                    <p className="text-fgdim">Nothing pending. The TTL sweeper will move things here as they age out.</p>
                </section>
            ) : (
                <section className="panel">
                    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                        {items.map((v) => (
                            <div key={v.id} className="bg-panel2 rounded-xl overflow-hidden border border-border">
                                <Link to={`/videos/${v.id}`} className="block no-underline">
                                    <div className="aspect-square bg-black">
                                        {v.thumbnail_url ? (
                                            <img src={v.thumbnail_url} alt="" className="w-full h-full object-cover" loading="lazy" />
                                        ) : (
                                            <div className="w-full h-full bg-gradient-to-br from-panel to-panel2" />
                                        )}
                                    </div>
                                    <div className="p-2.5">
                                        <div className="font-semibold text-sm leading-tight line-clamp-2 text-fg">{v.title || '(untitled)'}</div>
                                        <div className="text-xs text-fgdim mt-1">@{creatorByID.get(v.creator_id) ?? '?'}</div>
                                    </div>
                                </Link>
                                <div className="flex gap-1.5 px-2.5 pb-2.5">
                                    <button className="btn btn-primary text-xs flex-1" onClick={() => keep.mutate(v.id)} disabled={keep.isPending}>Keep</button>
                                    <button className="btn text-xs flex-1" onClick={() => restore.mutate(v.id)} disabled={restore.isPending}>Restore</button>
                                </div>
                            </div>
                        ))}
                    </div>
                </section>
            )}
        </div>
    )
}
