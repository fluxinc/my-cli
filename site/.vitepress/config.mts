import { defineConfig } from 'vitepress'

export default defineConfig({
  base: '/',
  lang: 'en-US',
  title: 'My AI',
  description: 'One manifest gives every AI harness the same skills, guidance, and context.',
  lastUpdated: true,
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg?v=2' }],
    ['link', { rel: 'manifest', href: '/site.webmanifest' }],
    ['meta', { name: 'theme-color', content: '#0f766e' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'My AI' }],
    ['meta', { property: 'og:title', content: 'My AI - One manifest for every AI harness' }],
    ['meta', { property: 'og:description', content: 'Install skills, generated guidance, mounts, and local context for every AI harness from one organization manifest.' }],
    ['meta', { property: 'og:url', content: 'https://my-cli.com/' }],
    ['meta', { property: 'og:image', content: 'https://my-cli.com/my-cli-glyph.svg?v=2' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:title', content: 'My AI - One manifest for every AI harness' }],
    ['meta', { name: 'twitter:description', content: 'One command gives every installed AI harness the same skills, context, and local tooling.' }],
    ['meta', { name: 'twitter:image', content: 'https://my-cli.com/my-cli-glyph.svg?v=2' }],
  ],
  themeConfig: {
    logo: {
      light: '/my-cli-wordmark.svg',
      dark: '/my-cli-wordmark-dark.svg',
      alt: 'My AI',
    },
    siteTitle: false,
    nav: [
      { text: 'Guide', link: '/guide/what-is-my-cli' },
      { text: 'CLI', link: '/guide/cli-reference' },
      {
        text: 'v0.30.1',
        items: [
          { text: 'Changelog', link: '/changelog' },
          { text: 'GitHub', link: 'https://github.com/fluxinc/my-cli' },
        ],
      },
    ],
    sidebar: [
      {
        text: 'Start',
        items: [
          { text: 'What is My AI?', link: '/guide/what-is-my-cli' },
          { text: 'Quickstart', link: '/guide/quickstart' },
          { text: 'Onboarding', link: '/guide/onboarding' },
          { text: 'The Model', link: '/guide/the-model' },
        ],
      },
      {
        text: 'Workspace',
        items: [
          { text: 'Manifests and Mounts', link: '/guide/manifest-and-mounts' },
          { text: 'Skills', link: '/guide/skills' },
          { text: 'Guidance and Contract', link: '/guide/guidance-and-contract' },
          { text: 'Services and Roles', link: '/guide/services-and-roles' },
        ],
      },
      {
        text: 'Daily Work',
        items: [
          { text: 'Records: Meetings, Support, Fleet', link: '/guide/records' },
          { text: 'Work Sessions', link: '/guide/sessions' },
          { text: 'Sync, Doctor, and Updates', link: '/guide/sync-and-doctor' },
        ],
      },
      {
        text: 'Administer',
        items: [
          { text: 'Admin', link: '/guide/admin' },
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
      { icon: 'github', link: 'https://github.com/fluxinc/my-cli' },
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
