import DefaultTheme from 'vitepress/theme'
import AnimatedHomeTitle from './AnimatedHomeTitle.vue'
import './custom.css'

export default {
  extends: DefaultTheme,
  Layout: AnimatedHomeTitle,
}
