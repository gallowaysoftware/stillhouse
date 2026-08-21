/// <reference types="vite/client" />

// Ambient Vite types. The repo had no .d.ts at all, so the side-effect
// import of index.css in main.tsx had nothing declaring it — TypeScript 5
// tolerated that, TypeScript 7 does not.
