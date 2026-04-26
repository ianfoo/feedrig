import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../api'

export default function Creators() {
    const qc = useQueryClient()
    const { data, isLoading, error } = useQuery({
        queryKey: ['creators'],
        queryFn: api.listCreators,
    })

    const [filter, setFilter] = useState('')
    const [handle, setHandle] = useState('')
    const [displayName, setDisplayName] = useState('')

    const addMutation = useMutation({
        mutationFn: () => api.addCreator(handle, displayName || undefined),
        onSuccess: () => {
            setHandle('')
            setDisplayName('')
            qc.invalidateQueries({ queryKey: ['creators'] })
        },
    })

    const deleteMutation = useMutation({
        mutationFn: (id: number) => api.deleteCreator(id),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['creators'] }),
    })

    const filtered = (data ?? []).filter((c) => {
        const q = filter.trim().toLowerCase()
        if (!q) return true
        return c.handle.toLowerCase().includes(q) || (c.display_name ?? '').toLowerCase().includes(q)
    })

    return (
        <div>
            <section className="panel">
                <h1 className="text-xl font-semibold mb-2">Creators</h1>
                <form
                    className="flex flex-col sm:flex-row gap-2"
                    onSubmit={(e) => { e.preventDefault(); if (handle.trim()) addMutation.mutate() }}
                >
                    <input className="input" placeholder="instagram handle or URL" value={handle} onChange={(e) => setHandle(e.target.value)} required />
                    <input className="input sm:max-w-xs" placeholder="display name (optional)" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
                    <button className="btn btn-primary" type="submit" disabled={addMutation.isPending}>Add</button>
                </form>
                {addMutation.error && <p className="text-danger text-sm mt-2">{(addMutation.error as Error).message}</p>}
                <p className="text-fgdim text-sm mt-3">Bulk add and Instagram data-export import are on the legacy <a href="/creators">/creators</a> page until they're ported here.</p>
            </section>

            <section className="panel">
                <div className="flex items-center justify-between mb-2">
                    <h2 className="text-base font-semibold">{filtered.length} creator{filtered.length !== 1 ? 's' : ''}</h2>
                    <input className="input max-w-[220px]" type="search" placeholder="Filter…" value={filter} onChange={(e) => setFilter(e.target.value)} />
                </div>
                {isLoading && <p className="text-fgdim">Loading…</p>}
                {error && <p className="text-danger">Failed to load: {(error as Error).message}</p>}
                {!isLoading && filtered.length === 0 && <p className="text-fgdim">No creators yet.</p>}
                <ul className="divide-y divide-border">
                    {filtered.map((c) => (
                        <li key={c.id} className="py-2.5 flex items-center gap-3 flex-wrap">
                            <Link to={`/creators/${c.id}`} className="text-fg no-underline flex items-baseline gap-2 flex-1 min-w-[200px]">
                                <span className="font-semibold">@{c.handle}</span>
                                {c.display_name && <span className="text-fgdim text-sm">{c.display_name}</span>}
                            </Link>
                            <span className="text-xs text-fgdim">
                                {c.last_fetched_at ? `fetched ${new Date(c.last_fetched_at * 1000).toLocaleDateString()}` : 'never fetched'}
                            </span>
                            <button
                                className="btn btn-danger text-xs"
                                onClick={() => { if (confirm(`Remove @${c.handle}?`)) deleteMutation.mutate(c.id) }}
                                disabled={deleteMutation.isPending}
                            >Remove</button>
                        </li>
                    ))}
                </ul>
            </section>
        </div>
    )
}
