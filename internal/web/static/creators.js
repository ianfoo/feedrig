// Client-side filter for the creators list. Matches against handle and
// display name (case-insensitive substring).
(() => {
    const input = document.getElementById('creator-filter');
    const list = document.getElementById('creator-list');
    if (!input || !list) return;

    function apply() {
        const q = input.value.trim().toLowerCase();
        for (const li of list.children) {
            const handle = (li.dataset.handle || '').toLowerCase();
            const name = (li.dataset.name || '').toLowerCase();
            li.hidden = q !== '' && !handle.includes(q) && !name.includes(q);
        }
    }
    input.addEventListener('input', apply);
})();
