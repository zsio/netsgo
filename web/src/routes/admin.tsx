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
      className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-5 p-4 sm:gap-6 sm:p-6 lg:p-8"
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, ease: 'easeOut' as const }}
    >
      <Outlet />
    </motion.div>
  );
}
