import { useState } from 'react';
import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import { useAdminConfig, useUpdateAdminConfig } from '@/hooks/use-admin-config';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Settings, Plus, Trash2 } from 'lucide-react';
import type { ServerConfig, PortRange } from '@/types';

export const adminConfigRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/config',
  component: AdminConfigPage,
});

function AdminConfigPage() {
  const { data: config, isLoading } = useAdminConfig();

  return (
    <div className="flex flex-col gap-6 w-full">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold tracking-tight">服务配置</h2>
      </div>

      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm p-6 shadow-sm overflow-hidden">
        {isLoading || !config ? (
          <div className="space-y-4">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-20 w-full" />
          </div>
        ) : (
          <AdminConfigForm key={JSON.stringify(config)} initialConfig={config} />
        )}
      </div>
    </div>
  );
}

function AdminConfigForm({ initialConfig }: { initialConfig: ServerConfig }) {
  const updateConfig = useUpdateAdminConfig();
  const [serverAddr, setServerAddr] = useState(initialConfig.server_addr);
  const [portRanges, setPortRanges] = useState<PortRange[]>(
    initialConfig.allowed_ports.length > 0 ? initialConfig.allowed_ports : []
  );
  const [saved, setSaved] = useState(false);

  const addPortRange = () => {
    setPortRanges([...portRanges, { start: 10000, end: 20000 }]);
  };

  const removePortRange = (index: number) => {
    setPortRanges(portRanges.filter((_, i) => i !== index));
  };

  const updatePortRange = (index: number, field: 'start' | 'end', value: number) => {
    const updated = [...portRanges];
    updated[index] = { ...updated[index], [field]: value };
    setPortRanges(updated);
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaved(false);

    try {
      await updateConfig.mutateAsync({
        server_addr: serverAddr,
        allowed_ports: portRanges,
      });
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    } catch (error) {
      console.error('保存配置失败', error);
    }
  };

  return (
    <form onSubmit={handleSave} className="flex flex-col gap-6">
      {/* 服务地址 */}
      <div className="flex flex-col gap-2">
        <label className="text-sm font-medium">对外服务地址</label>
        <Input
          value={serverAddr}
          onChange={(e) => setServerAddr(e.target.value)}
          placeholder="例如: https://tunnel.example.com"
        />
        <p className="text-xs text-muted-foreground">
          Agent 连接时使用的服务器公开地址。创建 Key 后生成的连接命令会使用此地址。
        </p>
      </div>

      {/* 端口白名单 */}
      <div className="flex flex-col gap-3">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium">允许穿透的端口范围</label>
          <Button type="button" variant="outline" size="sm" onClick={addPortRange} className="gap-1.5">
            <Plus className="w-3.5 h-3.5" /> 添加范围
          </Button>
        </div>

        {portRanges.length === 0 ? (
          <div className="text-sm text-muted-foreground bg-muted/30 rounded-md p-4 text-center">
            未设置端口范围 — 所有端口均允许穿透
          </div>
        ) : (
          <div className="space-y-3">
            {portRanges.map((range_, index) => (
              <div key={index} className="flex items-center gap-3">
                <Input
                  type="number"
                  min={1}
                  max={65535}
                  value={range_.start}
                  onChange={(e) => updatePortRange(index, 'start', Number.parseInt(e.target.value, 10) || 0)}
                  placeholder="起始端口"
                  className="w-32"
                />
                <span className="text-muted-foreground text-sm">—</span>
                <Input
                  type="number"
                  min={1}
                  max={65535}
                  value={range_.end}
                  onChange={(e) => updatePortRange(index, 'end', Number.parseInt(e.target.value, 10) || 0)}
                  placeholder="结束端口"
                  className="w-32"
                />
                <span className="text-xs text-muted-foreground flex-1">
                  {range_.start === range_.end
                    ? `单端口: ${range_.start}`
                    : `共 ${Math.max(0, range_.end - range_.start + 1)} 个端口`}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  onClick={() => removePortRange(index)}
                  className="text-muted-foreground hover:text-destructive shrink-0"
                >
                  <Trash2 className="w-4 h-4" />
                </Button>
              </div>
            ))}
          </div>
        )}
        <p className="text-xs text-muted-foreground">
          只有在白名单范围内的端口才能被 Agent 用于创建隧道映射。如果不设置任何范围，则所有端口均允许。
        </p>
      </div>

      {/* 保存按钮 */}
      <div className="border-t border-border/40 pt-4 mt-2 flex items-center justify-end gap-3">
        {saved && (
          <span className="text-sm text-emerald-500 font-medium animate-in fade-in">
            ✓ 已保存
          </span>
        )}
        <Button type="submit" disabled={updateConfig.isPending} className="gap-2">
          <Settings className="w-4 h-4" />
          {updateConfig.isPending ? '保存中...' : '保存配置'}
        </Button>
      </div>
    </form>
  );
}
