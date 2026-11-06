<script setup lang="ts">
import { withBase } from 'vitepress';

// A screenshot from public/screenshots (scripts/screenshots.sh), captured from
// the demo in both themes; the image follows the site theme. `mobile` shows
// the 390 px phone capture instead.
defineProps<{ name: string; alt: string; caption?: string; mobile?: boolean }>();
</script>

<template>
  <figure :class="['hp-shot', { 'hp-shot-mobile': mobile }]">
    <template v-if="mobile">
      <img :src="withBase(`/screenshots/${name}-mobile.png`)" :alt="alt" loading="lazy" width="390" height="844" />
    </template>
    <template v-else>
      <img class="hp-light" :src="withBase(`/screenshots/${name}-light.png`)" :alt="alt" loading="lazy" width="1440" height="900" />
      <img class="hp-dark" :src="withBase(`/screenshots/${name}-dark.png`)" :alt="alt" loading="lazy" width="1440" height="900" />
    </template>
    <figcaption v-if="caption">{{ caption }}</figcaption>
  </figure>
</template>
