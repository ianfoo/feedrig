import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../api'

export default function Groups() {
    const qc = useQueryClient()
    const { data, isLoading, error } = useQuery({ queryKey: ['groups'], queryFn: api.listGroups })

    const [name, setName] = useState('')
    const [recencyDays, setRecencyDays] = useState(7)
    const [includeTags, setIncludeTags] = useState('')
    const [excludeTags, setExcludeTags] = useState('')

    const create = useMutation({
        mutationFn: () => api.createGroup({
            name,
            recency_days: recencyDays,
            include_tags: includeTags.split(',').map((s) => s.trim()).filter(Boolean),
            exclude_tags: excludeTags.split(',').map((s) => s.trim()).filter(Boolean),
        }),
        onSuccess: () => {
            setName(''); setIncludeTags(''); setExcludeTags(''); setRecencyDays(7)
            qc.invalidateQueries({ queryKey: ['groups'] })
        },
    })

    const reorder = useMutation({
        mutationFn: (args: { slug: string; dir: 'up' | 'down' }) => api.reorderGroup(args.slug, args.dir),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['groups'] }),
    })

    return (
        <div>
            <section className="panel">
                <h1 className="text-xl font-semibold">Smart playlists</h1>
                <p className="text-fgdim text-sm mt-1">Group creators by topic; see what's new since you last looked, optionally filtered by tag.</p>
                <details className="mt-3">
                    <summary className="text-fgdim text-sm cursor-pointer">Create a group</summary>
                    <form
                        className="space-y-3 mt-3"
                        onSubmit={(e) => { e.preventDefault(); if (name.trim()) create.mutate() }}
                    >
                        <label className="block">
                            <span className="text-fgdim text-sm">Name</span>
                            <input className="input mt-1" value={name} onChange={(e) => setName(e.target.value)} placeholder="news, music, etc." required />
                        </label>
                        <label className="block">
                            <span className="text-fgdim text-sm">Recency window (days)</span>
                            <input type="number" min={1} max={365} className="input mt-1 max-w-[10rem]" value={recencyDays} onChange={(e) => setRecencyDays(parseInt(e.target.value, 10) || 7)} />
                        </label>
                        <label className="block">
                            <span className="text-fgdim text-sm">Include tags (comma-separated, optional)</span>
                            <input className="input mt-1" value={includeTags} onChange={(e) => setIncludeTags(e.target.value)} placeholder="news, political-commentary" />
                        </label>
                        <label className="block">
                            <span className="text-fgdim text-sm">Exclude tags (comma-separated, optional)</span>
                            <input className="input mt-1" value={excludeTags} onChange={(e) => setExcludeTags(e.target.value)} placeholder="crypto" />
                        </label>
                        <button type="submit" className="btn btn-primary" disabled={create.isPending}>Create</button>
                        {create.error && <p className="text-danger text-sm">{(create.error as Error).message}</p>}
                    </form>
                </details>
            </section>

            <section className="panel">
                {isLoading && <p className="text-fgdim">Loading…</p>}
                {error && <p className="text-danger">Failed: {(error as Error).message}</p>}
                {!isLoading && (data?.length ?? 0) === 0 && <p className="text-fgdim">No groups yet.</p>}
                <ul className="divide-y divide-border">
                    {(data ?? []).map((g) => (
                        <li key={g.id} className="py-2.5 flex items-center gap-2 flex-wrap">
                            <div className="flex flex-col gap-0.5 mr-1">
                                <button className="btn !py-0 !px-1 text-xs" title="Move up" onClick={() => reorder.mutate({ slug: g.slug, dir: 'up' })}>▲</button>
                                <button className="btn !py-0 !px-1 text-xs" title="Move down" onClick={() => reorder.mutate({ slug: g.slug, dir: 'down' })}>▼</button>
                            </div>
                            <Link to={`/groups/${g.slug}`} className="flex-1 min-w-[200px] text-fg no-underline">
                                <div className="font-semibold">{g.name}</div>
                                <div className="text-xs text-fgdim">
                                    {g.recency_days}d window
                                    {g.include_tags && g.include_tags.length > 0 && ` · only: ${g.include_tags.join(', ')}`}
                                    {g.exclude_tags && g.exclude_tags.length > 0 && ` · except: ${g.exclude_tags.join(', ')}`}
                                </div>
                            </Link>
                            <Link to={`/groups/${g.slug}/edit`} className="btn">Edit</Link>
                        </li>
                    ))}
                </ul>
            </section>
        </div>
    )
}
