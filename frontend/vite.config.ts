import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return;
          const after = id.split('node_modules/')[1];
          if (!after) return;
          const parts = after.split('/');
          const pkg = parts[0].startsWith('@')
            ? parts.slice(0, 2).join('/')
            : parts[0];
          if (
            pkg.startsWith('@tiptap/') ||
            pkg.startsWith('prosemirror-') ||
            pkg === 'rope-sequence' ||
            pkg === 'w3c-keyname' ||
            pkg === 'orderedmap' ||
            pkg === 'linkifyjs'
          ) {
            return 'editor';
          }
          if (
            pkg.startsWith('react-markdown') ||
            pkg.startsWith('remark-') ||
            pkg.startsWith('rehype-') ||
            pkg.startsWith('micromark') ||
            pkg.startsWith('mdast') ||
            pkg.startsWith('hast') ||
            pkg.startsWith('unist') ||
            pkg.startsWith('vfile') ||
            pkg === 'unified' ||
            pkg === 'highlight.js' ||
            pkg === 'lowlight' ||
            pkg === 'marked' ||
            pkg === 'markdown-table' ||
            pkg === 'property-information' ||
            pkg === 'space-separated-tokens' ||
            pkg === 'comma-separated-tokens' ||
            pkg === 'decode-named-character-reference' ||
            pkg === 'character-entities' ||
            pkg === 'character-reference-invalid' ||
            pkg === 'ccount' ||
            pkg === 'zwitch' ||
            pkg === 'trim-lines' ||
            pkg === 'bail' ||
            pkg === 'trough' ||
            pkg === 'is-plain-obj' ||
            pkg === 'style-to-object' ||
            pkg === 'inline-style-parser' ||
            pkg === 'web-namespaces' ||
            pkg === 'html-void-elements' ||
            pkg === 'stringify-entities' ||
            pkg === 'parse-entities'
          ) {
            return 'markdown';
          }
          if (pkg.startsWith('@xyflow/') || pkg.startsWith('d3-')) {
            return 'flow';
          }
          if (pkg === 'react' || pkg === 'react-dom' || pkg === 'scheduler') {
            return 'react';
          }
          if (
            pkg === 'lucide-react' ||
            pkg === 'xstate' ||
            pkg === 'zustand' ||
            pkg === '@cordisjs/core'
          ) {
            return 'app-vendor';
          }
          if (
            pkg === 'i18next' ||
            pkg === 'react-i18next' ||
            pkg === 'i18next-browser-languagedetector'
          ) {
            return 'i18n';
          }
        },
      },
    },
  },
});
