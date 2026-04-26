import { useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'

// The creator-detail page deliberately defers to the server-rendered HTML
// for now: per-creator video lists aren't in /api/v1/* yet, and rebuilding
// the fetch + cadence + history tabs is a meaningful bit of UX. The SPA
// shell shows a heads-up + a link.
export default function CreatorDetail() {
    const { id } = useParams<{ id: string }>()
    const { data: creators } = useQuery({ queryKey: ['creators'], queryFn: api.listCreators })
    const c = creators?.find((c) => c.id === Number(id))

    return (
        <section className="panel">
            <h1 className="text-xl font-semibold">@{c?.handle ?? id}</h1>
            {c?.display_name && <p className="text-fgdim mt-1">{c.display_name}</p>}
            <p className="text-fgdim text-sm mt-3">
                Per-creator history, Fetch-now, polling cadence, and the video grid live on the server-rendered page for now.
            </p>
            <a href={`/creators/${id}`} className="btn btn-primary mt-3 inline-block">Open the legacy view</a>
        </section>
    )
}
