// Capacitor config — wraps the Vite-built SPA into a native iOS / Android
// shell. The shell points at a running feedrig server (local or hosted)
// rather than bundling the backend; on first launch the user sets the URL.
//
// To bootstrap (one-time on a dev machine):
//
//   npm run build
//   npx cap init feedrig com.feedrig.app
//   npx cap add ios
//   npx cap add android
//   npx cap copy
//   npx cap open ios       # requires Xcode
//   npx cap open android   # requires Android Studio
//
// `webDir` points at the Vite output. For native builds, copy the SPA
// build into the platform projects with `npx cap copy` after each
// `npm run build`.

import type { CapacitorConfig } from '@capacitor/cli'

const config: CapacitorConfig = {
    appId: 'com.feedrig.app',
    appName: 'feedrig',
    webDir: '../internal/web/static/app',
    bundledWebRuntime: false,

    // The server config tells the wrapped WebView where to load from.
    // For dev: point at your feedrig host. For prod / store builds:
    // host the SPA inside the bundle by removing `server.url` and
    // configuring API base URLs explicitly in the SPA env.
    server: {
        // Override at deploy time; default is local development.
        url: 'http://localhost:7777/app/',
        cleartext: true,
    },

    ios: {
        contentInset: 'automatic',
    },

    android: {
        // Allow plain HTTP only when explicitly toggled (dev). Production
        // builds should hit the server over HTTPS.
        allowMixedContent: false,
    },
}

export default config
