import { defineConfig } from 'vitepress'

export default defineConfig({
  base: '/our-ai/',
  lang: 'en-US',
  title: 'Our AI',
  description: 'One manifest gives every AI harness the same skills, guidance, and context.',
  lastUpdated: true,
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/our-ai/favicon.svg?v=2' }],
    ['link', { rel: 'manifest', href: '/our-ai/site.webmanifest' }],
    ['meta', { name: 'theme-color', content: '#0f766e' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'Our AI' }],
    ['meta', { property: 'og:title', content: 'Our AI - One manifest for every AI harness' }],
    ['meta', { property: 'og:description', content: 'Install skills, generated guidance, mounts, and local context for every AI harness from one organization manifest.' }],
    ['meta', { property: 'og:url', content: 'https://fluxinc.github.io/our-ai/' }],
    ['meta', { property: 'og:image', content: 'https://fluxinc.github.io/our-ai/our-ai-glyph.svg?v=2' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:title', content: 'Our AI - One manifest for every AI harness' }],
    ['meta', { name: 'twitter:description', content: 'One command gives every installed AI harness the same skills, context, and local tooling.' }],
    ['meta', { name: 'twitter:image', content: 'https://fluxinc.github.io/our-ai/our-ai-glyph.svg?v=2' }],
  ],
  themeConfig: {
    logo: {
      light: '/our-ai-wordmark.svg',
      dark: '/our-ai-wordmark-dark.svg',
      alt: 'Our AI',
    },
    siteTitle: false,
    nav: [
      { text: 'Guide', link: '/guide/what-is-our-ai' },
      { text: 'CLI', link: '/guide/cli-reference' },
      {
        text: 'v0.19.0',
        items: [
          { text: 'Changelog', link: '/changelog' },
          { text: 'GitHub', link: 'https://github.com/fluxinc/our-ai' },
        ],
      },
    ],
    sidebar: [
      {
        text: 'Start',
        items: [
          { text: 'What is Our AI?', link: '/guide/what-is-our-ai' },
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
      { icon: 'github', link: 'https://github.com/fluxinc/our-ai' },
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
