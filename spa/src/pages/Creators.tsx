import { useState, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../api'
import ModePicker from '../components/ModePicker'
import type { IngestMode } from '../types'

export default function Creators() {
    const qc = useQueryClient()
    const { data, isLoading, error } = useQuery({
        queryKey: ['creators'],
        queryFn: api.listCreators,
    })

    const [filter, setFilter] = useState('')
    const [handle, setHandle] = useState('')
    const [displayName, setDisplayName] = useState('')
    const [ingestMode, setIngestMode] = useState<'full' | 'preview'>('full')

    const addMutation = useMutation({
        mutationFn: () => api.addCreator(handle, displayName || undefined, ingestMode),
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

    const fetchAllMutation = useMutation({
        mutationFn: () => api.fetchAll(),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['creators'] }),
    })

    const [bulkText, setBulkText] = useState('')
    const [bulkMode, setBulkMode] = useState<IngestMode>('full')
    const bulkFileRef = useRef<HTMLInputElement>(null)
    const bulkMutation = useMutation({
        mutationFn: async () => {
            let combined = bulkText
            const f = bulkFileRef.current?.files?.[0]
            if (f) {
                combined = (combined ? combined + '\n' : '') + (await f.text())
            }
            return api.bulkAddCreatorsWithMode(combined, bulkMode)
        },
        onSuccess: () => {
            setBulkText('')
            if (bulkFileRef.current) bulkFileRef.current.value = ''
            qc.invalidateQueries({ queryKey: ['creators'] })
        },
    })

    const importFileRef = useRef<HTMLInputElement>(null)
    const [importMsg, setImportMsg] = useState<string | null>(null)
    const importFollowing = async (e: React.FormEvent<HTMLFormElement>) => {
        e.preventDefault()
        const f = importFileRef.current?.files?.[0]
        if (!f) return
        const fd = new FormData()
        fd.append('file', f)
        setImportMsg('Importing…')
        // The legacy /creators/import endpoint accepts multipart and is
        // the natural one to call from the SPA — no need for a separate
        // JSON variant.
        const r = await fetch('/creators/import', { method: 'POST', body: fd, redirect: 'manual' })
        // Server redirects to /creators?flash=... which is fine; we surface the flash.
        const flash = new URLSearchParams((r.headers.get('Location') ?? '').split('?')[1] ?? '').get('flash')
        setImportMsg(flash ?? `${r.ok ? 'OK' : 'Failed'}: ${r.status}`)
        if (importFileRef.current) importFileRef.current.value = ''
        qc.invalidateQueries({ queryKey: ['creators'] })
    }

    type SortKey = 'handle' | 'added' | 'last_fetched' | 'followed' | 'mode'
    const [sortKey, setSortKey] = useState<SortKey>('handle')
    const [sortDesc, setSortDesc] = useState(false)
    const toggleSort = (k: SortKey) => {
        if (sortKey === k) setSortDesc(!sortDesc)
        else { setSortKey(k); setSortDesc(false) }
    }

    const filtered = (data ?? [])
        .filter((c) => {
            const q = filter.trim().toLowerCase()
            if (!q) return true
            return c.handle.toLowerCase().includes(q) || (c.display_name ?? '').toLowerCase().includes(q)
        })
        .sort((a, b) => {
            const dir = sortDesc ? -1 : 1
            switch (sortKey) {
                case 'handle': return dir * a.handle.localeCompare(b.handle)
                case 'added': return dir * ((a.added_at ?? 0) - (b.added_at ?? 0))
                case 'last_fetched': return dir * ((a.last_fetched_at ?? 0) - (b.last_fetched_at ?? 0))
                case 'followed': return dir * (((a as any).followed_at ?? 0) - ((b as any).followed_at ?? 0))
                case 'mode': return dir * (a.ingest_mode ?? 'full').localeCompare(b.ingest_mode ?? 'full')
            }
        })

    // Multi-select state for the "apply settings to selected" bulk path.
    const [selected, setSelected] = useState<Set<number>>(new Set())
    const toggleSel = (id: number) => {
        const n = new Set(selected)
        if (n.has(id)) n.delete(id); else n.add(id)
        setSelected(n)
    }
    const selectAllVisible = () => setSelected(new Set(filtered.map((c) => c.id)))
    const clearSelection = () => setSelected(new Set())

    const [bulkMode2, setBulkMode2] = useState<IngestMode | ''>('')
    const [bulkTTL, setBulkTTL] = useState<string>('')
    const [bulkCadence, setBulkCadence] = useState<string>('')

    const applyBulk = useMutation({
        mutationFn: async () => {
            const ids = [...selected]
            // Issue concurrent requests; the server handlers are independent.
            await Promise.all(ids.flatMap((id) => {
                const promises: Promise<unknown>[] = []
                if (bulkMode2 !== '') promises.push(api.setCreatorMode(id, bulkMode2))
                if (bulkTTL !== '') promises.push(api.setCreatorTTL(id, parseInt(bulkTTL, 10) || 0))
                if (bulkCadence !== '') promises.push(api.setCadence(id, parseInt(bulkCadence, 10) || 0))
                return promises
            }))
        },
        onSuccess: () => {
            setBulkMode2('')
            setBulkTTL('')
            setBulkCadence('')
            qc.invalidateQueries({ queryKey: ['creators'] })
        },
    })

    return (
        <div>
            <section className="panel">
                <h1 className="text-xl font-semibold mb-2">Creators</h1>
                <form
                    onSubmit={(e) => { e.preventDefault(); if (handle.trim()) addMutation.mutate() }}
                    className="space-y-2"
                >
                    <div className="flex flex-col sm:flex-row gap-2">
                        <input className="input" placeholder="instagram handle or URL" value={handle} onChange={(e) => setHandle(e.target.value)} required />
                        <input className="input sm:max-w-xs" placeholder="display name (optional)" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
                        <button className="btn btn-primary" type="submit" disabled={addMutation.isPending}>Add</button>
                    </div>
                    <ModePicker value={ingestMode} onChange={setIngestMode} />
                </form>
                {addMutation.error && <p className="text-danger text-sm mt-2">{(addMutation.error as Error).message}</p>}

                <details className="mt-4">
                    <summary className="text-fgdim text-sm cursor-pointer">Bulk add (paste a list or upload .txt / .csv)</summary>
                    <form className="mt-2 space-y-2" onSubmit={(e) => { e.preventDefault(); bulkMutation.mutate() }}>
                        <textarea
                            className="input"
                            rows={5}
                            value={bulkText}
                            onChange={(e) => setBulkText(e.target.value)}
                            placeholder={`One per line. Optionally "handle, Display Name" or "handle | preview" to override mode per-line.\nnatgeo, National Geographic\nsamhowell | preview\n# bass guitar:\nariadixongotbass`}
                        />
                        <ModePicker value={bulkMode} onChange={setBulkMode} label="Default mode" />
                        <div className="flex gap-2 items-center flex-wrap">
                            <input ref={bulkFileRef} type="file" accept=".txt,.csv,text/plain,text/csv" className="text-xs text-fgdim" />
                            <button type="submit" className="btn btn-primary" disabled={bulkMutation.isPending}>Add all</button>
                        </div>
                        {bulkMutation.data && (
                            <p className="text-fgdim text-sm">Added {bulkMutation.data.added}, skipped {bulkMutation.data.skipped}, failed {bulkMutation.data.failed}.</p>
                        )}
                    </form>
                </details>

                <details className="mt-2">
                    <summary className="text-fgdim text-sm cursor-pointer">Import from Instagram data export (following.json)</summary>
                    <p className="text-fgdim text-xs mt-2">Settings → Accounts Center → Your information and permissions → Download your information → JSON. Inside the zip, find <code>connections/followers_and_following/following.json</code> and upload it here. Followed-on dates are imported when present.</p>
                    <form className="mt-2 flex gap-2 items-center" onSubmit={importFollowing}>
                        <input ref={importFileRef} type="file" accept=".json,application/json" required className="text-xs text-fgdim" />
                        <button type="submit" className="btn btn-primary">Import</button>
                    </form>
                    {importMsg && <p className="text-fgdim text-sm mt-2">{importMsg}</p>}
                </details>
            </section>

            <section className="panel">
                <div className="flex items-center gap-2 flex-wrap mb-2">
                    <h2 className="text-base font-semibold">{filtered.length} creator{filtered.length !== 1 ? 's' : ''}</h2>
                    <input className="input max-w-[220px] ml-auto" type="search" placeholder="Filter…" value={filter} onChange={(e) => setFilter(e.target.value)} />
                    <button
                        className="btn btn-primary"
                        onClick={() => fetchAllMutation.mutate()}
                        disabled={fetchAllMutation.isPending || filtered.length === 0}
                    >Fetch all</button>
                </div>
                {fetchAllMutation.data && (
                    <p className="text-fgdim text-sm mb-2">Started {fetchAllMutation.data.started}, skipped {fetchAllMutation.data.skipped} already running.</p>
                )}
                {isLoading && <p className="text-fgdim">Loading…</p>}
                {error && <p className="text-danger">Failed to load: {(error as Error).message}</p>}
                {!isLoading && filtered.length === 0 && <p className="text-fgdim">No creators yet.</p>}

                {!isLoading && filtered.length > 0 && (
                    <>
                        <div className="flex gap-2 items-center text-xs text-fgdim mt-1 mb-2 flex-wrap">
                            <span>Sort:</span>
                            {(['handle', 'added', 'last_fetched', 'followed', 'mode'] as const).map((k) => (
                                <button
                                    key={k}
                                    className={`px-2 py-0.5 rounded ${sortKey === k ? 'bg-panel2 text-fg' : 'hover:bg-panel2'}`}
                                    onClick={() => toggleSort(k)}
                                >{k.replace('_', ' ')}{sortKey === k ? (sortDesc ? ' ↓' : ' ↑') : ''}</button>
                            ))}
                            <span className="ml-auto">
                                {selected.size > 0 ? `${selected.size} selected` : ''}
                                {selected.size === 0 && (
                                    <button className="underline" onClick={selectAllVisible}>select all</button>
                                )}
                                {selected.size > 0 && (
                                    <button className="underline ml-1" onClick={clearSelection}>clear</button>
                                )}
                            </span>
                        </div>

                        <ul className="divide-y divide-border">
                            {filtered.map((c) => (
                                <li key={c.id} className="py-2.5 flex items-center gap-2 flex-wrap">
                                    <input
                                        type="checkbox"
                                        checked={selected.has(c.id)}
                                        onChange={() => toggleSel(c.id)}
                                        aria-label={`select @${c.handle}`}
                                    />
                                    <Link to={`/creators/${c.id}`} className="text-fg no-underline flex items-baseline gap-2 flex-1 min-w-[160px]">
                                        <span className="font-semibold">@{c.handle}</span>
                                        {c.display_name && <span className="text-fgdim text-sm">{c.display_name}</span>}
                                        {c.ingest_mode === 'preview' && <span className="text-xs px-1.5 rounded-full bg-accent text-[#0b1220]">preview</span>}
                                    </Link>
                                    <span className="text-xs text-fgdim whitespace-nowrap">
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
                    </>
                )}
            </section>

            {selected.size > 0 && (
                <section className="panel sticky bottom-16 md:bottom-3 border-accent shadow-lg z-20 bg-panel">
                    <h3 className="text-sm font-semibold mb-2">Apply to {selected.size} selected</h3>
                    <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
                        <label className="text-xs text-fgdim">
                            Ingest mode
                            <select className="input mt-0.5" value={bulkMode2} onChange={(e) => setBulkMode2(e.target.value as IngestMode | '')}>
                                <option value="">— don't change —</option>
                                <option value="full">full</option>
                                <option value="preview">preview</option>
                            </select>
                        </label>
                        <label className="text-xs text-fgdim">
                            TTL override (days; 0 = clear)
                            <input className="input mt-0.5" type="number" min={0} max={3650} value={bulkTTL} onChange={(e) => setBulkTTL(e.target.value)} placeholder="leave blank to skip" />
                        </label>
                        <label className="text-xs text-fgdim">
                            Polling cadence (hours; 0 = clear)
                            <input className="input mt-0.5" type="number" min={0} max={168} value={bulkCadence} onChange={(e) => setBulkCadence(e.target.value)} placeholder="leave blank to skip" />
                        </label>
                    </div>
                    <div className="flex gap-2 mt-3 flex-wrap">
                        <button
                            className="btn btn-primary"
                            onClick={() => applyBulk.mutate()}
                            disabled={applyBulk.isPending || (bulkMode2 === '' && bulkTTL === '' && bulkCadence === '')}
                        >Apply</button>
                        {applyBulk.isSuccess && <span className="text-ok text-sm self-center">Applied to {selected.size}.</span>}
                    </div>
                </section>
            )}
        </div>
    )
}
