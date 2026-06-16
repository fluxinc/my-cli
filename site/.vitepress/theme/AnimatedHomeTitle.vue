<script setup lang="ts">
import DefaultTheme from 'vitepress/theme'
import { nextTick, onMounted, onUnmounted, watch } from 'vue'
import { useRoute } from 'vitepress'

const { Layout } = DefaultTheme

const baseTitle = '> my ai'
const stablePrefix = '> my'
const titles = [
  '> my ai claude-code',
  '> my meetings',
  '> my fleet',
  '> my work',
  '> my ai codex',
  '> my ai agy',
]

const eraseDelay = 24
const typeDelay = 34
const baseHold = 5000
const variantHold = 2300

const route = useRoute()
let active = false
let currentTitle = baseTitle
let variantsSinceBase = 0
let returnToBaseAfter = 2
let timer: ReturnType<typeof window.setTimeout> | undefined
let observer: MutationObserver | undefined
let titleEl: HTMLElement | undefined
let cursorEl: HTMLElement | undefined

function prefersReducedMotion() {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function wait(ms: number) {
  return new Promise<void>((resolve) => {
    timer = window.setTimeout(resolve, ms)
  })
}

function pickVariant() {
  const choices = titles.filter((title) => title !== currentTitle)
  return choices[Math.floor(Math.random() * choices.length)] || titles[0]
}

function nextTitle() {
  if (currentTitle === baseTitle) {
    returnToBaseAfter = Math.random() < 0.5 ? 2 : 3
    variantsSinceBase = 0
    return pickVariant()
  }

  variantsSinceBase += 1
  if (variantsSinceBase >= returnToBaseAfter) {
    return baseTitle
  }

  return pickVariant()
}

function ensureCursor(el: HTMLElement) {
  if (cursorEl?.isConnected) {
    return
  }

  cursorEl = document.createElement('span')
  cursorEl.className = 'my-home-title-cursor'
  cursorEl.setAttribute('aria-hidden', 'true')
  el.append(cursorEl)
}

function setTitle(value: string) {
  if (!titleEl) {
    return
  }
  titleEl.firstChild?.remove()
  titleEl.prepend(document.createTextNode(value))
  ensureCursor(titleEl)
}

async function eraseToPrefix() {
  while (active && currentTitle.length > stablePrefix.length) {
    currentTitle = currentTitle.slice(0, -1)
    setTitle(currentTitle)
    await wait(eraseDelay)
  }
}

async function typeTo(target: string) {
  while (active && currentTitle.length < target.length) {
    currentTitle = target.slice(0, currentTitle.length + 1)
    setTitle(currentTitle)
    await wait(typeDelay)
  }
}

async function runLoop() {
  if (!titleEl || prefersReducedMotion()) {
    return
  }

  setTitle(baseTitle)
  currentTitle = baseTitle

  while (active) {
    await wait(currentTitle === baseTitle ? baseHold : variantHold)
    if (!active) {
      return
    }

    const target = nextTitle()
    await eraseToPrefix()
    await typeTo(target)
  }
}

function stopAnimation() {
  active = false
  if (timer !== undefined) {
    window.clearTimeout(timer)
    timer = undefined
  }
  observer?.disconnect()
  observer = undefined
  cursorEl?.remove()
  cursorEl = undefined
  titleEl?.classList.remove('my-home-title')
  titleEl = undefined
}

function findTitle() {
  return document.querySelector<HTMLElement>('.VPHomeHero .name')
}

function startAnimation() {
  stopAnimation()

  if (route.path !== '/') {
    return
  }

  const start = () => {
    const el = findTitle()
    if (!el) {
      return false
    }

    titleEl = el
    titleEl.classList.add('my-home-title')
    ensureCursor(titleEl)
    active = true
    void runLoop()
    return true
  }

  if (start()) {
    return
  }

  observer = new MutationObserver(() => {
    if (start()) {
      observer?.disconnect()
      observer = undefined
    }
  })
  observer.observe(document.body, { childList: true, subtree: true })
}

onMounted(() => {
  void nextTick(startAnimation)
})

watch(
  () => route.path,
  () => {
    void nextTick(startAnimation)
  },
)

onUnmounted(stopAnimation)
</script>

<template>
  <Layout />
</template>
