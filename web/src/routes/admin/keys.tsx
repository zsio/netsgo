import { useState } from 'react';
import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import { useAdminKeys, useCreateAPIKey } from '@/hooks/use-admin-keys';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Key } from 'lucide-react';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';

export const adminKeysRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/keys',
  component: AdminKeysPage,
});

function AdminKeysPage() {
  const { data: keys = [], isLoading } = useAdminKeys();
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [createdRawKey, setCreatedRawKey] = useState('');
  const createKey = useCreateAPIKey();

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newKeyName.trim()) return;
    try {
      const res = await createKey.mutateAsync({ name: newKeyName, permissions: ['all'] });
      setCreatedRawKey(res.raw_key);
      setNewKeyName('');
    } catch (err) {
      console.error(err);
    }
  };

  const resetDialog = () => {
    setIsDialogOpen(false);
    setCreatedRawKey('');
    setNewKeyName('');
  };

  return (
    <div className="flex flex-col gap-6 max-w-5xl mx-auto">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold tracking-tight">API Key 管理</h2>
        <Dialog open={isDialogOpen} onOpenChange={(open) => {
          if (!open) resetDialog();
          else setIsDialogOpen(true);
        }}>
          <DialogTrigger asChild>
            <Button className="gap-2">
              <Key className="w-4 h-4" /> 新建 Key
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>新建 API Key</DialogTitle>
            </DialogHeader>
            {createdRawKey ? (
              <div className="flex flex-col gap-4 py-4">
                <div className="bg-amber-500/10 text-amber-500 p-3 rounded-md text-sm">
                  请立即复制并妥善保存此 Key，刷新后将无法再次查看！
                </div>
                <div className="p-3 bg-muted font-mono text-sm break-all rounded-md select-all">
                  {createdRawKey}
                </div>
                <Button onClick={resetDialog}>完成</Button>
              </div>
            ) : (
              <form onSubmit={handleCreate} className="flex flex-col gap-4 py-4">
                <div className="space-y-2">
                  <label className="text-sm font-medium">名称 / 用途</label>
                  <Input 
                    placeholder="例如: CI/CD 部署密钥" 
                    value={newKeyName} 
                    onChange={(e) => setNewKeyName(e.target.value)} 
                    required 
                    autoFocus
                  />
                </div>
                <Button type="submit" disabled={createKey.isPending}>
                  {createKey.isPending ? '创建中...' : '创 建'}
                </Button>
              </form>
            )}
          </DialogContent>
        </Dialog>
      </div>

      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden">
        <table className="w-full text-sm text-left">
          <thead className="text-xs text-muted-foreground bg-muted/50 uppercase">
            <tr>
              <th className="px-6 py-3 font-medium">名称</th>
              <th className="px-6 py-3 font-medium">ID (短)</th>
              <th className="px-6 py-3 font-medium">创建时间</th>
              <th className="px-6 py-3 font-medium">状态</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border/40">
            {isLoading ? (
              <tr><td colSpan={4} className="p-4"><Skeleton className="h-10 w-full" /></td></tr>
            ) : keys.length === 0 ? (
              <tr><td colSpan={4} className="px-6 py-8 text-center text-muted-foreground">空空如也，系统当前处于无需 Key 的开放模式</td></tr>
            ) : (
              keys.map(k => (
                <tr key={k.id} className="hover:bg-muted/30">
                  <td className="px-6 py-3 font-medium">{k.name}</td>
                  <td className="px-6 py-3 font-mono text-xs text-muted-foreground">{k.id.split('-')[0]}</td>
                  <td className="px-6 py-3 text-muted-foreground">{new Date(k.created_at).toLocaleString()}</td>
                  <td className="px-6 py-3">
                    {k.is_active ? (
                      <span className="text-emerald-500 font-medium">已启用</span>
                    ) : (
                      <span className="text-muted-foreground">已禁用</span>
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
