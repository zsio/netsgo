import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import { Users, Info } from 'lucide-react';

export const adminAccountsRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/accounts',
  component: AdminAccountsPage,
});

function AdminAccountsPage() {
  return (
    <div className="flex flex-col gap-6 max-w-5xl mx-auto h-[calc(100vh-140px)]">
      <div className="flex items-center justify-between shrink-0">
        <h2 className="text-2xl font-bold tracking-tight">账号管理</h2>
      </div>

      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm overflow-hidden flex-1 flex flex-col items-center justify-center p-8 text-center text-muted-foreground">
        <Users className="w-12 h-12 mb-4 opacity-50" />
        <h3 className="text-lg font-medium text-foreground mb-2">多账号管理</h3>
        <p className="text-sm max-w-md flex flex-col gap-2">
          <span>暂未开放完整的多用户 CRUD 操作界面。</span>
          <span className="flex items-center justify-center gap-1.5 bg-amber-500/10 text-amber-500 px-3 py-1.5 rounded-md">
            <Info className="w-4 h-4" /> 您当前使用的是系统初始化生成的管理账号。
          </span>
        </p>
      </div>
    </div>
  );
}
