import { createRoute, Outlet } from '@tanstack/react-router';
import { motion } from 'motion/react';
import { dashboardRoute } from './dashboard';

export const adminRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/admin',
  component: AdminLayout,
});

function AdminLayout() {
  return (
    <motion.div
      className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-6 z-10"
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, ease: 'easeOut' as const }}
    >
      <Outlet />
    </motion.div>
  );
}
