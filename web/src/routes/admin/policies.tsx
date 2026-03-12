import { useState, useEffect } from 'react';
import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import { useAdminPolicies, useUpdateAdminPolicies } from '@/hooks/use-admin-policies';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Shield } from 'lucide-react';
import type { TunnelPolicy } from '@/types';

export const adminPoliciesRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/policies',
  component: AdminPoliciesPage,
});

function AdminPoliciesPage() {
  const { data: policy, isLoading } = useAdminPolicies();
  const updatePolicy = useUpdateAdminPolicies();

  const [formState, setFormState] = useState<TunnelPolicy>({
    min_port: 0,
    max_port: 0,
    blocked_ports: [],
    agent_whitelist: [],
  });
  
  const [blockedPortsStr, setBlockedPortsStr] = useState('');
  const [whitelistStr, setWhitelistStr] = useState('');

  useEffect(() => {
    if (policy) {
      setFormState(policy);
      setBlockedPortsStr(policy.blocked_ports?.join(', ') || '');
      setWhitelistStr(policy.agent_whitelist?.join(', ') || '');
    }
  }, [policy]);

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const ports = blockedPortsStr.split(',').map(p => parseInt(p.trim())).filter(p => !isNaN(p));
      const hosts = whitelistStr.split(',').map(h => h.trim()).filter(h => h.length > 0);
      
      const payload: TunnelPolicy = {
        ...formState,
        blocked_ports: ports,
        agent_whitelist: hosts,
      };
      
      await updatePolicy.mutateAsync(payload);
    } catch (err) {
      console.error('保存策略失败', err);
    }
  };

  return (
    <div className="flex flex-col gap-6 max-w-4xl mx-auto">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold tracking-tight">网络与安全策略</h2>
      </div>

      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm p-6 shadow-sm overflow-hidden">
        {isLoading ? (
          <div className="space-y-4">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-20 w-full" />
          </div>
        ) : (
          <form onSubmit={handleSave} className="flex flex-col gap-6">
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-2">
                <label className="text-sm font-medium">允许的最小端口号</label>
                <Input 
                  type="number" 
                  value={formState.min_port || ''} 
                  onChange={e => setFormState({...formState, min_port: parseInt(e.target.value) || 0})}
                  placeholder="如: 10000 (0 表示不限制)" 
                />
              </div>
              <div className="space-y-2">
                <label className="text-sm font-medium">允许的最大端口号</label>
                <Input 
                  type="number" 
                  value={formState.max_port || ''} 
                  onChange={e => setFormState({...formState, max_port: parseInt(e.target.value) || 0})}
                  placeholder="如: 60000 (0 表示不限制)" 
                />
              </div>
            </div>

            <div className="space-y-2">
              <label className="text-sm font-medium">黑名单端口 (逗号分隔)</label>
              <Input 
                value={blockedPortsStr} 
                onChange={e => setBlockedPortsStr(e.target.value)}
                placeholder="例如: 22, 80, 443" 
              />
              <p className="text-xs text-muted-foreground">黑名单中的端口禁止 Agent 创建映射隧道。</p>
            </div>

            <div className="space-y-2">
              <label className="text-sm font-medium">Agent 节点白名单 (Hostname, 逗号分隔)</label>
              <Input 
                value={whitelistStr} 
                onChange={e => setWhitelistStr(e.target.value)}
                placeholder="例如: staging-server, prod-worker-1" 
              />
              <p className="text-xs text-muted-foreground">如果留空，则所有认证成功的 Agent 都可建立连接。如果配置，则只有主机名在白名单内的 Agent 可建立代理隧道。</p>
            </div>

            <div className="border-t border-border/40 pt-4 mt-2 flex justify-end">
              <Button type="submit" disabled={updatePolicy.isPending} className="gap-2">
                <Shield className="w-4 h-4" />
                {updatePolicy.isPending ? '保存中...' : '保存策略'}
              </Button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
