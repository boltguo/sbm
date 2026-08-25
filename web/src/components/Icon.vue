<script setup lang="ts">
import {
  Check,
  Copy,
  Download,
  Eye,
  EyeOff,
  Grid2X2,
  LogOut,
  Pencil,
  Plus,
  Power,
  RotateCcw,
  Route,
  Server,
  SlidersHorizontal,
  Trash2,
  X,
} from '@lucide/vue'
import GithubMark from './GithubMark.vue'

type IconName = 'grid' | 'route' | 'server' | 'sliders' | 'copy' | 'download' | 'refresh' | 'power' | 'logout' | 'plus' | 'x' | 'edit' | 'trash' | 'check' | 'eye' | 'eye-off' | 'github'

defineProps<{ name: IconName }>()

const icons = {
  grid: Grid2X2,
  route: Route,
  server: Server,
  sliders: SlidersHorizontal,
  copy: Copy,
  download: Download,
  refresh: RotateCcw,
  power: Power,
  logout: LogOut,
  plus: Plus,
  x: X,
  edit: Pencil,
  trash: Trash2,
  check: Check,
  eye: Eye,
  'eye-off': EyeOff,
  github: GithubMark,
}
</script>

<template>
  <component :is="icons[name]" class="icon" :stroke-width="1.8" aria-hidden="true" />
</template>
