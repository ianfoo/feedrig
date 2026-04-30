import { useParams, useNavigate, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useEffect } from 'react'
import { api } from '../api'

type Membership = 'none' | 'include' | 'exclude'

export default function GroupEdit() {
    const { slug } = useParams<{ slug: string }>()
    const navigate = useNavigate()
    const qc = useQueryClient()

    const { data, isLoading, error } = useQuery({
        queryKey: ['group', slug],
        queryFn: () => api.getGroup(slug!),
    })
    const { data: creators } = useQuery({ queryKey: ['creators'], queryFn: api.listCreators })

    const [name, setName] = useState('')
    const [recencyDays, setRecencyDays] = useState(7)
    const [includeTags, setIncludeTags] = useState('')
    const [excludeTags, setExcludeTags] = useState('')
    const [memberships, setMemberships] = useState<Map<number, Membership>>(new Map())

    useEffect(() => {
        if (!data) return
        setName(data.group.name)
        setRecencyDays(data.group.recency_days)
        setIncludeTags((data.group.include_tags ?? []).join(', '))
        setExcludeTags((data.group.exclude_tags ?? []).join(', '))
        const m = new Map<number, Membership>()
        for (const x of data.members) {
            m.set(x.creator_id, x.excluded ? 'exclude' : 'include')
        }
        setMemberships(m)
    }, [data])

    const updateMutation = useMutation({
        mutationFn: () => api.updateGroup(slug!, {
            name,
            recency_days: recencyDays,
            include_tags: includeTags.split(',').map((s) => s.trim()).filter(Boolean),
            exclude_tags: excludeTags.split(',').map((s) => s.trim()).filter(Boolean),
        }),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['group', slug] }),
    })

    const membersMutation = useMutation({
        mutationFn: () => {
            const include: number[] = []
            const exclude: number[] = []
            for (const [id, role] of memberships) {
                if (role === 'include') include.push(id)
                else if (role === 'exclude') exclude.push(id)
            }
            return api.setGroupMembers(slug!, include, exclude)
        },
        onSuccess: () => qc.invalidateQueries({ queryKey: ['group', slug] }),
    })

    const deleteMutation = useMutation({
        mutationFn: () => api.deleteGroup(slug!),
        onSuccess: () => navigate('/groups'),
    })

    if (isLoading) return <div className="panel">Loading…</div>
    if (error) return <div className="panel text-danger">Failed: {(error as Error).message}</div>
    if (!data) return null

    return (
        <div>
            <section className="panel">
                <h1 className="text-xl font-semibold">Edit: {data.group.name}</h1>
                <form
                    className="space-y-3 mt-3"
                    onSubmit={(e) => { e.preventDefault(); updateMutation.mutate() }}
                >
                    <label className="block">
                        <span className="text-fgdim text-sm">Name</span>
                        <input className="input mt-1" value={name} onChange={(e) => setName(e.target.value)} required />
                    </label>
                    <label className="block">
                        <span className="text-fgdim text-sm">Recency window (days)</span>
                        <input type="number" min={1} max={365} className="input mt-1 max-w-[10rem]" value={recencyDays} onChange={(e) => setRecencyDays(parseInt(e.target.value, 10) || 7)} />
                    </label>
                    <label className="block">
                        <span className="text-fgdim text-sm">Include tags (comma-separated)</span>
                        <input className="input mt-1" value={includeTags} onChange={(e) => setIncludeTags(e.target.value)} />
                    </label>
                    <label className="block">
                        <span className="text-fgdim text-sm">Exclude tags (comma-separated)</span>
                        <input className="input mt-1" value={excludeTags} onChange={(e) => setExcludeTags(e.target.value)} />
                    </label>
                    <button type="submit" className="btn btn-primary" disabled={updateMutation.isPending}>Save settings</button>
                    {updateMutation.isSuccess && <span className="text-ok text-sm ml-2">Saved.</span>}
                </form>
            </section>

            <section className="panel">
                <h2 className="text-base font-semibold mb-2">Members</h2>
                <p className="text-fgdim text-sm mb-3">Pick which creators belong to this group. "Exclude" subtracts a creator (useful when they overlap with another group but you don't want them showing here).</p>
                <ul className="divide-y divide-border max-h-[60vh] overflow-y-auto">
                    {(creators ?? []).map((c) => {
                        const role = memberships.get(c.id) ?? 'none'
                        return (
                            <li key={c.id} className="py-2 flex items-center gap-3 flex-wrap">
                                <span className="font-semibold min-w-[8rem]">@{c.handle}</span>
                                {c.display_name && <span className="text-fgdim text-sm flex-1 min-w-0">{c.display_name}</span>}
                                <div className="flex gap-2 text-sm">
                                    {(['none', 'include', 'exclude'] as Membership[]).map((opt) => (
                                        <label key={opt} className="flex items-center gap-1 text-fgdim">
                                            <input
                                                type="radio"
                                                name={`m-${c.id}`}
                                                checked={role === opt}
                                                onChange={() => {
                                                    const next = new Map(memberships)
                                                    if (opt === 'none') next.delete(c.id)
                                                    else next.set(c.id, opt)
                                                    setMemberships(next)
                                                }}
                                            />
                                            <span>{opt}</span>
                                        </label>
                                    ))}
                                </div>
                            </li>
                        )
                    })}
                </ul>
                <div className="mt-3">
                    <button className="btn btn-primary" onClick={() => membersMutation.mutate()} disabled={membersMutation.isPending}>Save members</button>
                    {membersMutation.isSuccess && <span className="text-ok text-sm ml-2">Saved.</span>}
                </div>
            </section>

            <section className="panel">
                <details>
                    <summary className="text-danger cursor-pointer">Danger zone</summary>
                    <button
                        className="btn btn-danger mt-3"
                        onClick={() => { if (confirm('Delete this group?')) deleteMutation.mutate() }}
                        disabled={deleteMutation.isPending}
                    >Delete group</button>
                </details>
            </section>

            <section className="panel">
                <p className="text-fgdim text-sm"><Link to={`/groups/${slug}`}>← back to feed</Link> · <Link to="/groups">all groups</Link></p>
            </section>
        </div>
    )
}
