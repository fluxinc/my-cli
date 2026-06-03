import { defineConfig } from 'vitepress'

export default defineConfig({
  base: '/flux/',
  lang: 'en-US',
  title: 'flux',
  description: 'Manifest-backed AI workspace tooling',
  lastUpdated: true,
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/flux/favicon.svg' }],
    ['link', { rel: 'manifest', href: '/flux/site.webmanifest' }],
    ['meta', { name: 'theme-color', content: '#0f766e' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'flux' }],
    ['meta', { property: 'og:title', content: 'flux - Manifest-backed AI workspace tooling' }],
    ['meta', { property: 'og:description', content: 'Install skills, generated guidance, mounts, and local context for every AI harness from one manifest.' }],
    ['meta', { property: 'og:url', content: 'https://fluxinc.github.io/flux/' }],
    ['meta', { property: 'og:image', content: 'https://fluxinc.github.io/flux/flux-glyph.svg' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:title', content: 'flux - Manifest-backed AI workspace tooling' }],
    ['meta', { name: 'twitter:description', content: 'One command gives every installed AI harness the same skills, context, and local tooling.' }],
    ['meta', { name: 'twitter:image', content: 'https://fluxinc.github.io/flux/flux-glyph.svg' }],
  ],
  themeConfig: {
    logo: '/flux-glyph.svg',
    nav: [
      { text: 'Guide', link: '/guide/what-is-flux' },
      { text: 'CLI', link: '/guide/cli-reference' },
      {
        text: 'v0.2.0',
        items: [
          { text: 'Changelog', link: '/changelog' },
          { text: 'GitHub', link: 'https://github.com/fluxinc/flux' },
        ],
      },
    ],
    sidebar: [
      {
        text: 'Start',
        items: [
          { text: 'What is flux?', link: '/guide/what-is-flux' },
          { text: 'Quickstart', link: '/guide/quickstart' },
          { text: 'The Model', link: '/guide/the-model' },
        ],
      },
      {
        text: 'Operate',
        items: [
          { text: 'Skills', link: '/guide/skills' },
          { text: 'Admin', link: '/guide/admin' },
          { text: 'Manifests and Mounts', link: '/guide/manifest-and-mounts' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'CLI Reference', link: '/guide/cli-reference' },
          { text: 'Changelog', link: '/changelog' },
        ],
      },
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/fluxinc/flux' },
    ],
    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright (c) 2026 Flux Inc.',
    },
    search: {
      provider: 'local',
    },
  },
})
