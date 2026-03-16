import { motion } from 'motion/react';
import { OverviewHeader } from './OverviewHeader';
import { ServerInfoCard } from './ServerInfoCard';
import { DashboardClientTable } from './DashboardClientTable';
import { DashboardTunnelTable } from './DashboardTunnelTable';

const stagger = {
  hidden: {},
  show: { transition: { staggerChildren: 0.08 } },
};

const fadeUp = {
  hidden: { opacity: 0, y: 12 },
  show: { opacity: 1, y: 0, transition: { duration: 0.35, ease: 'easeOut' as const } },
};

export function OverviewPage() {
  return (
    <motion.div
      className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10"
      variants={stagger}
      initial="hidden"
      animate="show"
    >
      <motion.div variants={fadeUp}><OverviewHeader /></motion.div>
      <motion.div variants={fadeUp}><ServerInfoCard /></motion.div>
      <motion.div variants={fadeUp}><DashboardClientTable /></motion.div>
      <motion.div variants={fadeUp}><DashboardTunnelTable /></motion.div>
    </motion.div>
  );
}
