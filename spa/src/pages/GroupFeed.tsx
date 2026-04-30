import { useParams, useSearchParams, Link } from 'react-router-dom'
import { useQuery, useQueries } from '@tanstack/react-query'
import { api } from '../api'
import VideoCard from '../components/VideoCard'

export default function GroupFeed() {
    const { slug } = useParams<{ slug: string }>()
    const [searchParams, setSearchParams] = useSearchParams()
    const onlyUnseen = searchParams.get('unseen') === '1'
    const tag = searchParams.get('tag') ?? undefined

    const { data, isLoading, error } = useQuery({
        queryKey: ['group-feed', slug, onlyUnseen, tag],
        queryFn: () => api.groupFeed(slug!, { unseen: onlyUnseen, tag }),
    })

    // Resolve creator handles for the cards. List once and look up.
    const { data: creators } = useQuery({ queryKey: ['creators'], queryFn: api.listCreators })
    const creatorByID = new Map((creators ?? []).map((c) => [c.id, c.handle]))

    // Fetch full video details (with summaries + tags) for the visible feed.
    const detailQueries = useQueries({
        queries: (data?.videos ?? []).map((v) => ({
            queryKey: ['video', v.id],
            queryFn: () => api.getVideo(v.id),
            staleTime: 60_000,
        })),
    })

    const setFlag = (k: string, v?: string) => {
        const next = new URLSearchParams(searchParams)
        if (v) next.set(k, v); else next.delete(k)
        setSearchParams(next, { replace: true })
    }

    if (isLoading) return <div className="panel">Loading…</div>
    if (error) return <div className="panel text-danger">Failed: {(error as Error).message}</div>
    if (!data) return null

    return (
        <div>
            <section className="panel">
                <div className="flex justify-between items-start gap-3 flex-wrap">
                    <div>
                        <h1 className="text-xl font-semibold">{data.group.name}</h1>
                        <p className="text-fgdim text-sm mt-1">
                            Last {data.group.recency_days} days
                            {data.group.include_tags && data.group.include_tags.length > 0 && (
                                <> · only: {data.group.include_tags.map((t) => <span key={t} className="tag ml-1">{t}</span>)}</>
                            )}
                            {data.group.exclude_tags && data.group.exclude_tags.length > 0 && (
                                <> · except: {data.group.exclude_tags.map((t) => <span key={t} className="tag ml-1">{t}</span>)}</>
                            )}
                        </p>
                    </div>
                    <div className="flex gap-1.5 text-sm">
                        <a className="btn" href={`/groups/${slug}/digest`}>Digest</a>
                        <a className="btn" href={`/groups/${slug}/rss`} title="RSS feed">RSS</a>
                        <Link className="btn" to={`/groups/${slug}/edit`}>Edit</Link>
                    </div>
                </div>

                <div className="flex gap-1.5 flex-wrap mt-3 text-sm">
                    <button className={`btn ${!onlyUnseen ? 'btn-primary' : ''}`} onClick={() => setFlag('unseen')}>All</button>
                    <button className={`btn ${onlyUnseen ? 'btn-primary' : ''}`} onClick={() => setFlag('unseen', '1')}>Unseen since last visit</button>
                    {tag && (
                        <span className="badge badge-saved">
                            tag: {tag}
                            <button className="ml-1 hover:opacity-70" onClick={() => setFlag('tag')}>×</button>
                        </span>
                    )}
                </div>
            </section>

            <section className="panel">
                {data.videos.length === 0 ? (
                    <p className="text-fgdim">No matching videos.</p>
                ) : (
                    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                        {data.videos.map((v, i) => {
                            const enriched = detailQueries[i]?.data ?? v
                            return (
                                <VideoCard
                                    key={v.id}
                                    video={enriched}
                                    creator={creatorByID.get(enriched.creator_id)}
                                />
                            )
                        })}
                    </div>
                )}
            </section>

            <section className="panel">
                <p className="text-fgdim text-sm">
                    <Link to="/groups">← back to groups</Link>
                </p>
            </section>
        </div>
    )
}
