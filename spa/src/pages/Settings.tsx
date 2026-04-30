import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import type { Settings as SettingsT } from '../api'

export default function Settings() {
    const qc = useQueryClient()
    const { data, isLoading, error } = useQuery({
        queryKey: ['settings'],
        queryFn: api.getSettings,
    })

    const [form, setForm] = useState<Partial<SettingsT>>({})

    useEffect(() => {
        if (data) setForm({ ...data })
    }, [data])

    const save = useMutation({
        mutationFn: () => api.putSettings(form),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['settings'] }),
    })

    if (isLoading) return <div className="panel">Loading…</div>
    if (error) return <div className="panel text-danger">Failed: {(error as Error).message}</div>

    const update = (k: keyof SettingsT) => (e: React.ChangeEvent<HTMLInputElement>) => {
        const v = e.target.value === '' ? undefined : parseInt(e.target.value, 10)
        setForm((prev) => ({ ...prev, [k]: v }))
    }

    return (
        <div>
            <section className="panel">
                <h1 className="text-xl font-semibold">Settings</h1>
                {save.isSuccess && <p className="text-ok text-sm mt-2">Saved.</p>}
                {save.error && <p className="text-danger text-sm mt-2">{(save.error as Error).message}</p>}

                <form onSubmit={(e) => { e.preventDefault(); save.mutate() }} className="mt-4 space-y-5">
                    <fieldset className="border border-border rounded-lg p-3">
                        <legend className="text-fgdim text-sm px-1">Polling cadence</legend>
                        <label className="block">
                            <span className="text-fgdim text-sm">Default poll interval (hours)</span>
                            <input className="input mt-1 max-w-[12rem]" type="number" min={1} max={168} value={form.poll_hours ?? ''} onChange={update('poll_hours')} />
                        </label>
                        <p className="text-fgdim text-xs mt-2">How often each creator's profile is checked. Per-creator overrides on the creator page.</p>
                    </fieldset>

                    <fieldset className="border border-border rounded-lg p-3">
                        <legend className="text-fgdim text-sm px-1">Self-curating library</legend>
                        <label className="block mb-3">
                            <span className="text-fgdim text-sm">Auto-expire active videos after (days)</span>
                            <input className="input mt-1 max-w-[12rem]" type="number" min={1} max={365} value={form.ttl_days ?? ''} onChange={update('ttl_days')} />
                        </label>
                        <label className="block mb-3">
                            <span className="text-fgdim text-sm">Grace window before media file removal (days)</span>
                            <input className="input mt-1 max-w-[12rem]" type="number" min={1} max={60} value={form.grace_days ?? ''} onChange={update('grace_days')} />
                        </label>
                        <label className="block">
                            <span className="text-fgdim text-sm">Metadata TTL on archived rows (days; 0 = keep forever)</span>
                            <input className="input mt-1 max-w-[12rem]" type="number" min={0} max={3650} value={form.metadata_ttl_days ?? 0} onChange={update('metadata_ttl_days')} />
                        </label>
                        <p className="text-fgdim text-xs mt-2">After grace, videos move to "archived" — file removed but title, description, summary, transcript, tags, thumbnail kept. Metadata TTL caps how long archived rows linger before being hard-purged.</p>
                    </fieldset>

                    <button type="submit" className="btn btn-primary" disabled={save.isPending}>Save</button>
                </form>
            </section>
        </div>
    )
}
