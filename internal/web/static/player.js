// Player wiring: speed controls, keyboard nav, position persistence.
(() => {
    const shell = document.querySelector('.player-shell');
    if (!shell) return;
    const video = document.getElementById('player');
    if (!video) return;

    const videoID = shell.dataset.videoId;
    const resume = parseFloat(shell.dataset.resume || '0');
    const prevURL = shell.dataset.prevUrl || '';
    const nextURL = shell.dataset.nextUrl || '';

    // Resume from last position if more than a couple seconds in.
    video.addEventListener('loadedmetadata', () => {
        if (resume > 2 && resume < video.duration - 2) {
            video.currentTime = resume;
        }
        markActiveSpeed(video.playbackRate);
    });

    // Speed buttons. Set both defaultPlaybackRate (sticks across some
    // browser-driven resets) and playbackRate (the active rate). Re-apply
    // on loadeddata since some browsers reset playbackRate when metadata
    // arrives. Also visually flash the button so it's obvious the click
    // registered, since on macOS Safari the audio rate-change is subtle.
    let currentSpeed = 1.0;
    const speedBtns = document.querySelectorAll('.speed-btn');
    function applySpeed(s) {
        currentSpeed = s;
        try {
            video.defaultPlaybackRate = s;
            video.playbackRate = s;
        } catch (e) {
            console.warn('playbackRate set failed', e);
        }
        markActiveSpeed(s);
    }
    speedBtns.forEach(btn => {
        btn.addEventListener('click', (ev) => {
            ev.preventDefault();
            const s = parseFloat(btn.dataset.speed);
            if (!isFinite(s) || s <= 0) return;
            applySpeed(s);
        });
    });
    function markActiveSpeed(speed) {
        speedBtns.forEach(b => {
            b.classList.toggle('active', Math.abs(parseFloat(b.dataset.speed) - speed) < 0.001);
        });
    }
    // Some browsers reset playbackRate on loadeddata; re-apply.
    video.addEventListener('loadeddata', () => applySpeed(currentSpeed));
    video.addEventListener('ratechange', () => markActiveSpeed(video.playbackRate));

    // Position persistence: throttle to once per 4s + on pause/end.
    let lastSent = 0;
    function persist(watched = false) {
        const body = new URLSearchParams({ position: video.currentTime.toFixed(2), watched: watched ? '1' : '0' });
        navigator.sendBeacon
            ? navigator.sendBeacon(`/videos/${videoID}/position`, body)
            : fetch(`/videos/${videoID}/position`, { method: 'POST', body, keepalive: true });
    }
    video.addEventListener('timeupdate', () => {
        const now = Date.now();
        if (now - lastSent > 4000) {
            lastSent = now;
            persist(false);
        }
    });
    video.addEventListener('pause', () => persist(false));
    video.addEventListener('ended', () => {
        persist(true);
        if (nextURL) window.location.href = nextURL;
    });
    window.addEventListener('beforeunload', () => persist(video.duration > 0 && video.currentTime / video.duration > 0.9));

    // Keyboard nav.
    //
    // Naked ArrowLeft/ArrowRight are intentionally NOT trapped — they fall
    // through to the native <video> element's 5s-seek handler. Use
    // Shift/Alt+arrow to navigate between videos. Wheel-scroll-to-next was
    // removed: it conflicted with ordinary page scrolling for the
    // transcript / caption blocks below the player.
    const speedSequence = [0.75, 1.0, 1.25, 1.5, 1.75, 2.0, 2.5];
    document.addEventListener('keydown', (e) => {
        // Ignore when typing in form fields.
        if (e.target.matches('input, textarea, select')) return;

        switch (e.key) {
            case ' ':
                e.preventDefault();
                video.paused ? video.play() : video.pause();
                break;
            case 'ArrowLeft':
                if (e.shiftKey || e.altKey) {
                    e.preventDefault();
                    if (prevURL) window.location.href = prevURL;
                }
                // else: let the native video element seek -5s.
                break;
            case 'ArrowRight':
                if (e.shiftKey || e.altKey) {
                    e.preventDefault();
                    if (nextURL) window.location.href = nextURL;
                }
                // else: let the native video element seek +5s.
                break;
            case 'j':
            case 'J':
                e.preventDefault();
                video.currentTime = Math.max(0, video.currentTime - 5);
                break;
            case 'l':
            case 'L':
                e.preventDefault();
                video.currentTime = Math.min(video.duration, video.currentTime + 5);
                break;
            case 'k':
            case 'K':
                e.preventDefault();
                video.paused ? video.play() : video.pause();
                break;
            default:
                if (e.key >= '1' && e.key <= '7') {
                    const idx = parseInt(e.key, 10) - 1;
                    if (idx < speedSequence.length) {
                        applySpeed(speedSequence[idx]);
                    }
                }
        }
    });
})();
