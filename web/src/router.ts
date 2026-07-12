import { createRouter, createWebHistory } from 'vue-router'

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', redirect: '/types' },
    { path: '/types', component: () => import('./pages/TypesPage.vue') },
    { path: '/types/:id', component: () => import('./pages/TypeDetailPage.vue') },
    { path: '/entities', component: () => import('./pages/EntitiesPage.vue') },
    { path: '/entities/:typeId/:entityId', component: () => import('./pages/EntityDetailPage.vue') },
    { path: '/delivery', component: () => import('./pages/DeliveryPage.vue') },
    { path: '/graphql', component: () => import('./pages/GraphQLPage.vue') },
    { path: '/activity', component: () => import('./pages/ActivityPage.vue') },
    { path: '/settings', component: () => import('./pages/SettingsPage.vue') },
    { path: '/:pathMatch(.*)*', component: () => import('./pages/NotFoundPage.vue') },
  ],
})
