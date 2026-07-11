<script setup lang="ts">
import EmptyState from '@/components/ui/EmptyState.vue'
import Button from '@/components/ui/Button.vue'
import { RouterLink } from 'vue-router'
</script>

<template>
  <div class="mt-16">
    <EmptyState title="Page not found" body="The address doesn't match anything in the console.">
      <template #action>
        <RouterLink to="/types"><Button variant="primary">Back to types</Button></RouterLink>
      </template>
    </EmptyState>
  </div>
</template>
