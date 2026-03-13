import { useState } from 'react';
import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import {
  useAdminKeys,
  useCreateAPIKey,
  useDeleteAPIKey,
  useDisableAPIKey,
  useEnableAPIKey,
} from '@/hooks/use-admin-keys';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Key } from 'lucide-react';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';

export const adminKeysRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/keys',
  component: AdminKeysPage,
});

const EXPIRY_OPTIONS = [
  { label: '不限制', value: '' },
  { label: '1 小时', value: '1h' },
  { label: '3 小时', value: '3h' },
  { label: '24 小时', value: '24h' },
  { label: '7 天', value: '168h' },
];

function AdminKeysPage() {
  const { data: keys = [], isLoading } = useAdminKeys();
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [expiresIn, setExpiresIn] = useState('');
  const [maxUses, setMaxUses] = useState(0);
  const [createdRawKey, setCreatedRawKey] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string } | null>(null);
  const createKey = useCreateAPIKey();
  const enableKey = useEnableAPIKey();
  const disableKey = useDisableAPIKey();
  const deleteKey = useDeleteAPIKey();

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newKeyName.trim()) {
      return;
    }

    try {
      const response = await createKey.mutateAsync({
        name: newKeyName,
        permissions: ['connect'],
        expires_in: expiresIn || undefined,
        max_uses: maxUses > 0 ? maxUses : undefined,
      });
      setCreatedRawKey(response.raw_key);
      setNewKeyName('');
      setExpiresIn('');
      setMaxUses(0);
    } catch (error) {
      console.error('create api key failed', error);
    }
  };

  const resetDialog = () => {
    setIsDialogOpen(false);
    setCreatedRawKey('');
    setNewKeyName('');
    setExpiresIn('');
    setMaxUses(0);
  };

  const formatExpiry = (expiresAt?: string) => {
    if (!expiresAt) return '永不过期';
    const d = new Date(expiresAt);
    if (d < new Date()) return '已过期';
    return d.toLocaleString();
  };

  const formatUsage = (useCount: number, maxUses: number) => {
    if (maxUses === 0) return `${useCount} / ∞`;
    return `${useCount} / ${maxUses}`;
  };

  return (
    <>
      <div className="flex flex-col gap-6 w-full">
        <div className="flex items-center justify-between">
          <h2 className="text-2xl font-bold tracking-tight">API Key 管理</h2>
          <Dialog open={isDialogOpen} onOpenChange={(open) => {
            if (!open) {
              resetDialog();
              return;
            }
            setIsDialogOpen(true);
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
                    请立即复制并妥善保存此 Key，刷新后将无法再次查看。
                  </div>
                  <div className="p-3 bg-muted font-mono text-sm break-all rounded-md select-all">
                    {createdRawKey}
                  </div>
                  <Button onClick={resetDialog}>完成</Button>
                </div>
              ) : (
                <form onSubmit={handleCreate} className="flex flex-col gap-4 py-4">
                  <div className="flex flex-col gap-2">
                    <label className="text-sm font-medium">名称 / 用途</label>
                    <Input
                      placeholder="例如: staging-agent"
                      value={newKeyName}
                      onChange={(e) => setNewKeyName(e.target.value)}
                      required
                      autoFocus
                    />
                  </div>
                  <div className="flex flex-col gap-2">
                    <label className="text-sm font-medium">过期时间</label>
                    <select
                      value={expiresIn}
                      onChange={(e) => setExpiresIn(e.target.value)}
                      className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                    >
                      {EXPIRY_OPTIONS.map((opt) => (
                        <option key={opt.value} value={opt.value}>{opt.label}</option>
                      ))}
                    </select>
                  </div>
                  <div className="flex flex-col gap-2">
                    <label className="text-sm font-medium">最大使用次数</label>
                    <Input
                      type="number"
                      min={0}
                      value={maxUses || ''}
                      onChange={(e) => setMaxUses(Number.parseInt(e.target.value, 10) || 0)}
                      placeholder="0 表示不限制"
                    />
                    <p className="text-xs text-muted-foreground">每次 Agent 连接将消耗一次使用次数。0 表示不限制。</p>
                  </div>
                  <div className="text-xs text-muted-foreground">当前阶段仅支持 `connect` 权限。</div>
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
                <th className="px-6 py-3 font-medium">权限</th>
                <th className="px-6 py-3 font-medium">使用量</th>
                <th className="px-6 py-3 font-medium">过期时间</th>
                <th className="px-6 py-3 font-medium">状态</th>
                <th className="px-6 py-3 font-medium text-right">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/40">
              {isLoading ? (
                <tr><td colSpan={6} className="p-4"><Skeleton className="h-10 w-full" /></td></tr>
              ) : keys.length === 0 ? (
                <tr><td colSpan={6} className="px-6 py-8 text-center text-muted-foreground">暂无 Agent API Key</td></tr>
              ) : (
                keys.map((key) => (
                  <tr key={key.id} className="hover:bg-muted/30">
                    <td className="px-6 py-3 font-medium">{key.name}</td>
                    <td className="px-6 py-3 text-muted-foreground">{key.permissions.join(', ')}</td>
                    <td className="px-6 py-3 text-muted-foreground tabular-nums">
                      {formatUsage(key.use_count, key.max_uses)}
                    </td>
                    <td className="px-6 py-3 text-muted-foreground">
                      <span className={key.expires_at && new Date(key.expires_at) < new Date() ? 'text-destructive' : ''}>
                        {formatExpiry(key.expires_at)}
                      </span>
                    </td>
                    <td className="px-6 py-3">
                      {key.is_active ? (
                        <span className="text-emerald-500 font-medium">已启用</span>
                      ) : (
                        <span className="text-muted-foreground">已禁用</span>
                      )}
                    </td>
                    <td className="px-6 py-3">
                      <div className="flex items-center justify-end gap-2">
                        {key.is_active ? (
                          <Button variant="outline" size="sm" onClick={() => disableKey.mutate(key.id)}>
                            禁用
                          </Button>
                        ) : (
                          <Button variant="outline" size="sm" onClick={() => enableKey.mutate(key.id)}>
                            启用
                          </Button>
                        )}
                        <Button variant="destructive" size="sm" onClick={() => setDeleteTarget({ id: key.id, name: key.name })}>
                          删除
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      <ConfirmDialog
        open={deleteTarget !== null}
        title="删除 API Key"
        description={`确认删除 API Key「${deleteTarget?.name ?? ''}」？删除后依赖该 Key 的 Agent 将无法继续认证。`}
        confirmLabel="删除"
        variant="destructive"
        onCancel={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!deleteTarget) {
            return;
          }
          deleteKey.mutate(deleteTarget.id);
          setDeleteTarget(null);
        }}
      />
    </>
  );
}
