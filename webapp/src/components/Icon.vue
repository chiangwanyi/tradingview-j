<script setup lang="ts">
/** 极简线性图标集：16px 描边图标，避免为了几个图标引一个图标库。 */
const PATHS = {
  pass: ['M3 7l9-4 9 4v10l-9 4-9-4z', 'M3 7l9 4 9-4', 'M12 11v10'],
  chart: ['M3 20h18', 'M7 20v-6', 'M12 20v-10', 'M17 20v-4'],
  trophy: ['M8 4h8v5a4 4 0 0 1-8 0z', 'M8 5H5v2a3 3 0 0 0 3 3', 'M16 5h3v2a3 3 0 0 1-3 3', 'M12 13v5', 'M9 20h6'],
  dollar: [
    'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18',
    'M12 7v10',
    'M14.5 10a2.5 2.5 0 0 0-5 0c0 1.5 5 1.5 5 3a2.5 2.5 0 0 1-5 0',
  ],
  award: ['M12 4a5 5 0 1 0 0 10 5 5 0 0 0 0-10', 'M9 14l-2 7 5-3 5 3-2-7'],
  grid: ['M4 4h6v6H4z', 'M14 4h6v6h-6z', 'M4 14h6v6H4z', 'M14 14h6v6h-6z'],
  user: ['M12 4a4 4 0 1 0 0 8 4 4 0 0 0 0-8', 'M4 21a8 8 0 0 1 16 0'],
  plus: ['M12 5v14', 'M5 12h14'],
  star: ['M12 3.5l2.6 5.4 5.9.8-4.3 4.2 1 5.9-5.2-2.8-5.2 2.8 1-5.9L3.5 9.7l5.9-.8z'],
  left: ['M15 18l-6-6 6-6'],
  right: ['M9 18l6-6-6-6'],
  logout: ['M15 4h4v16h-4', 'M11 8l-4 4 4 4', 'M7 12h9'],
  trash: ['M4 7h16', 'M9 7V4h6v3', 'M6 7l1 14h10l1-14'],
  close: ['M6 6l12 12', 'M18 6L6 18'],
  check: ['M4 12l5 5L20 6'],
} as const

const props = withDefaults(defineProps<{ name: keyof typeof PATHS; size?: number }>(), { size: 16 })
</script>

<template>
  <svg
    :width="props.size"
    :height="props.size"
    viewBox="0 0 24 24"
    :fill="props.name === 'star' ? 'currentColor' : 'none'"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path v-for="d in PATHS[props.name]" :key="d" :d="d" />
  </svg>
</template>
