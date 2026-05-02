import type { IngestMode } from '../types'

// Two-state radio: full = download videos as they arrive (the default).
// preview = fetch only metadata + thumbnail; the user promotes to a full
// download on demand. Useful for high-volume creators where you want to
// triage by caption/thumb before committing disk space.
export default function ModePicker({
    value, onChange, label = 'Ingest mode',
}: {
    value: IngestMode
    onChange: (m: IngestMode) => void
    label?: string
}) {
    return (
        <div className="flex items-center gap-3 text-sm flex-wrap">
            <span className="text-fgdim">{label}:</span>
            {(['full', 'preview'] as IngestMode[]).map((m) => (
                <label key={m} className="flex items-center gap-1 cursor-pointer">
                    <input
                        type="radio"
                        name={`mode-${label}`}
                        checked={value === m}
                        onChange={() => onChange(m)}
                    />
                    <span className={value === m ? 'text-fg font-medium' : 'text-fgdim'}>
                        {m === 'full' ? 'Full (download videos)' : 'Preview (caption + thumbnail only)'}
                    </span>
                </label>
            ))}
        </div>
    )
}
