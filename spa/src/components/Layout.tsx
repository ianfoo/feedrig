import { NavLink } from 'react-router-dom'
import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'

const tabs: { to: string; label: string }[] = [
    { to: '/creators', label: 'Creators' },
    { to: '/groups', label: 'Groups' },
    { to: '/search', label: 'Search' },
    { to: '/pending', label: 'Pending' },
    { to: '/settings', label: 'Settings' },
]

export default function Layout({ children }: { children: ReactNode }) {
    const { data: stats } = useQuery({
        queryKey: ['stats'],
        queryFn: api.getStats,
        refetchInterval: 30_000,
    })

    return (
        <div className="min-h-full pb-16 md:pb-0">
            <header className="sticky top-0 z-10 bg-panel/95 backdrop-blur border-b border-border">
                <div className="mx-auto max-w-3xl flex items-center gap-3 px-4 py-2.5">
                    <NavLink to="/creators" className="flex items-center gap-2 font-semibold text-fg no-underline hover:no-underline">
                        <CraneLogo className="w-5 h-5 text-accent" />
                        <span>feedrig</span>
                    </NavLink>
                    <nav className="ml-auto hidden md:flex gap-3 text-sm items-center">
                        {tabs.map((t) => (
                            <NavLink key={t.to} to={t.to} className={({ isActive }) =>
                                'px-2 py-1 rounded-md ' +
                                (isActive ? 'text-fg bg-panel2' : 'text-fgdim hover:text-fg')
                            }>
                                {t.label}
                                {t.to === '/pending' && stats && stats.pending_count > 0 && (
                                    <span className="ml-1 inline-block bg-warn text-[#2a1f00] rounded-full px-1.5 text-xs font-semibold">{stats.pending_count}</span>
                                )}
                            </NavLink>
                        ))}
                        {stats && (
                            <span className="text-xs text-fgdim bg-panel2 border border-border rounded-full px-2 py-0.5" title={`${stats.video_count} videos`}>
                                {stats.media_bytes_human}
                            </span>
                        )}
                    </nav>
                </div>
            </header>

            <main className="mx-auto max-w-3xl px-4 py-4">
                {children}
            </main>

            {/* Mobile bottom nav. Shown < md. Just first 3 tabs to fit. */}
            <nav className="md:hidden fixed bottom-0 inset-x-0 bg-panel/95 backdrop-blur border-t border-border z-10">
                <div className="grid grid-cols-5">
                    {tabs.map((t) => (
                        <NavLink key={t.to} to={t.to} className={({ isActive }) =>
                            'py-2.5 text-center text-xs ' +
                            (isActive ? 'text-fg bg-panel2' : 'text-fgdim')
                        }>
                            <div>{t.label}</div>
                            {t.to === '/pending' && stats && stats.pending_count > 0 && (
                                <div className="text-warn text-[10px] font-semibold">{stats.pending_count}</div>
                            )}
                        </NavLink>
                    ))}
                </div>
            </nav>
        </div>
    )
}

function CraneLogo({ className }: { className?: string }) {
    return (
        <svg viewBox="0 0 64 64" fill="none" stroke="currentColor" strokeWidth={4} strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden>
            <line x1="14" y1="6" x2="14" y2="58" />
            <line x1="14" y1="14" x2="56" y2="14" />
            <line x1="14" y1="6" x2="56" y2="14" />
            <line x1="46" y1="14" x2="46" y2="32" />
            <path d="M40 32 L46 40 L52 32 Z" fill="currentColor" />
            <line x1="2" y1="58" x2="62" y2="58" />
        </svg>
    )
}
