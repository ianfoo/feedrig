import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../api'

export default function Groups() {
    const { data, isLoading, error } = useQuery({
        queryKey: ['groups'],
        queryFn: api.listGroups,
    })

    return (
        <div>
            <section className="panel">
                <h1 className="text-xl font-semibold">Smart playlists</h1>
                <p className="text-fgdim text-sm mt-1">Group creators by topic, see what's new since you last looked, optionally filtered by tag. Group creation and editing live on the legacy <a href="/groups">/groups</a> page until they're ported.</p>
            </section>

            <section className="panel">
                {isLoading && <p className="text-fgdim">Loading…</p>}
                {error && <p className="text-danger">Failed: {(error as Error).message}</p>}
                {!isLoading && (data?.length ?? 0) === 0 && <p className="text-fgdim">No groups yet.</p>}
                <ul className="divide-y divide-border">
                    {(data ?? []).map((g) => (
                        <li key={g.id} className="py-2.5 flex items-center gap-3 flex-wrap">
                            <Link to={`/groups/${g.slug}`} className="flex-1 min-w-[200px] text-fg no-underline">
                                <div className="font-semibold">{g.name}</div>
                                <div className="text-xs text-fgdim">
                                    {g.recency_days}d window
                                    {g.include_tags && g.include_tags.length > 0 && ` · only: ${g.include_tags.join(', ')}`}
                                    {g.exclude_tags && g.exclude_tags.length > 0 && ` · except: ${g.exclude_tags.join(', ')}`}
                                </div>
                            </Link>
                            <a href={`/groups/${g.slug}/edit`} className="btn">Edit</a>
                        </li>
                    ))}
                </ul>
            </section>
        </div>
    )
}
