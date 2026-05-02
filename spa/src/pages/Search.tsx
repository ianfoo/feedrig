import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'

interface Hit {
    video_id: number
    title: string
    creator: string
    matched_in: string
    excerpt: string
    state: string
}

async function search(q: string): Promise<Hit[]> {
    const r = await fetch('/api/v1/search?q=' + encodeURIComponent(q))
    if (!r.ok) throw new Error(await r.text() || r.statusText)
    return r.json()
}

export default function Search() {
    const [params, setParams] = useSearchParams()
    const [q, setQ] = useState(params.get('q') ?? '')
    const activeQ = params.get('q') ?? ''

    const { data, isFetching, error } = useQuery({
        queryKey: ['search', activeQ],
        queryFn: () => search(activeQ),
        enabled: activeQ.length > 0,
    })

    return (
        <div>
            <section className="panel">
                <h1 className="text-xl font-semibold">Search the corpus</h1>
                <p className="text-fgdim text-sm mt-1">Across video titles, descriptions, summaries, and transcripts.</p>
                <form
                    className="flex gap-2 mt-3"
                    onSubmit={(e) => { e.preventDefault(); setParams(q.trim() ? { q: q.trim() } : {}) }}
                >
                    <input className="input" autoFocus placeholder="bass guitar, election, pizza dough…" value={q} onChange={(e) => setQ(e.target.value)} />
                    <button className="btn btn-primary" type="submit">Search</button>
                </form>
            </section>

            {activeQ && (
                <section className="panel">
                    {isFetching && <p className="text-fgdim">Searching…</p>}
                    {error && <p className="text-danger">Failed: {(error as Error).message}</p>}
                    {!isFetching && (data?.length ?? 0) === 0 && <p className="text-fgdim">No matches for {activeQ}.</p>}
                    <ul className="divide-y divide-border">
                        {(data ?? []).map((h) => (
                            <li key={h.video_id} className="py-2.5">
                                <Link to={`/videos/${h.video_id}`} className="block group no-underline">
                                    <div className="font-semibold text-fg group-hover:text-accent">{h.title || '(untitled)'}</div>
                                    <div className="text-xs text-fgdim mt-0.5">@{h.creator} · matched in <span className="text-accent">{h.matched_in}</span> · state: {h.state}</div>
                                    <div className="text-sm text-fg mt-1 leading-snug">{h.excerpt}</div>
                                </Link>
                            </li>
                        ))}
                    </ul>
                </section>
            )}
        </div>
    )
}
