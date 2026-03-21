import { useState } from 'react';
import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import { useAdminConfig, useUpdateAdminConfig } from '@/hooks/use-admin-config';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Plus, AlertTriangle, ShieldAlert, Edit2, Check, X, Trash2 } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader,
  AlertDialogMedia, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { api } from '@/lib/api';
import { getServerAddrValidationError, normalizeServerAddr, SERVER_ADDR_HELP_TEXT, SERVER_ADDR_PLACEHOLDER } from '@/lib/server-address';
import { createLocalId } from '@/lib/utils';
import toast from 'react-hot-toast';
import type { ServerConfig, PortRange } from '@/types';

export const adminConfigRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/config',
  component: AdminConfigPage,
});

/** 受端口白名单变更影响的隧道 */
interface AffectedTunnel {
  client_id: string;
  hostname: string;
  display_name?: string;
  tunnel_name: string;
  remote_port: number;
  status: string;
}

type LocalPortRange = PortRange & { _id: string };
type DisplayRangeRow = LocalPortRange | { isAdding: true };

function AdminConfigPage() {
  const { data: config, isLoading } = useAdminConfig();

  return (
    <div className="flex flex-col gap-6 w-full pb-10">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold tracking-tight">服务配置</h2>
          <p className="text-sm text-muted-foreground mt-1">
            配置服务端的基础信息与安全控制策略。
          </p>
        </div>
      </div>

      {isLoading || !config ? (
        <div className="space-y-4">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : (
        <AdminConfigForm key={JSON.stringify(config)} initialConfig={config} />
      )}
    </div>
  );
}

function AdminConfigForm({ initialConfig }: { initialConfig: ServerConfig }) {
  const updateConfig = useUpdateAdminConfig();
  const [serverAddr, setServerAddr] = useState(initialConfig.server_addr || '');
  // 为每行分配一个绝对稳定的本地 id 以保证增删、编辑改值时 React 动画不会重组闪烁
  const [portRanges, setPortRanges] = useState<LocalPortRange[]>(() => {
    const ports = initialConfig.allowed_ports || [];
    return ports.map(p => ({ ...p, _id: createLocalId('port-range') }));
  });
  
  // 单行编辑状态管理
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [editForm, setEditForm] = useState<{ start: number | '', end: number | '' }>({ start: '', end: '' });

  const [saved, setSaved] = useState(false);
  const [checking, setChecking] = useState(false);
  const [affectedTunnels, setAffectedTunnels] = useState<AffectedTunnel[]>([]);
  const [showConfirm, setShowConfirm] = useState(false);

  const toPayloadPorts = (ranges: LocalPortRange[]): PortRange[] =>
    ranges.map((range) => ({ start: range.start, end: range.end }));

  // --- 端口表单交互逻辑 ---
  const startAdd = () => {
    setEditingIndex(portRanges.length); // index = length 表示新增
    setEditForm({ start: '', end: '' });
  };

  const startEdit = (index: number) => {
    setEditingIndex(index);
    setEditForm(portRanges[index]);
  };

  const cancelEdit = () => {
    setEditingIndex(null);
  };

  const saveEdit = () => {
    const startVal = Number(editForm.start);
    const endVal = Number(editForm.end);

    // 基础校验
    if (!startVal || !endVal || startVal < 1 || endVal > 65535) {
      toast.error('请完整填写且介于 1 - 65535 之间');
      return;
    }
    if (startVal > endVal) {
      toast.error('起始端口不能大于结束端口');
      return;
    }
    // 冲突重叠校验
    const isOverlap = portRanges.some((range, i) => {
      if (i === editingIndex) return false;
      return Math.max(startVal, range.start) <= Math.min(endVal, range.end);
    });

    if (isOverlap) {
      toast.error('与现有的端口规则存在重叠区间');
      return;
    }

    const updated = [...portRanges];
    // 使用保持既有的 _id（若是新增行，则创建一个新的 UUID）
    const existingId = editingIndex !== null && updated[editingIndex] ? updated[editingIndex]._id : createLocalId('port-range');
    updated[editingIndex!] = { start: startVal, end: endVal, _id: existingId };
    setPortRanges(updated);
    setEditingIndex(null);
  };

  const handleInputKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault(); // 阻止触发表单整体提交
      saveEdit();
    } else if (e.key === 'Escape') {
      cancelEdit();
    }
  };

  const removePortRange = (index: number) => {
    setPortRanges(portRanges.filter((_, i) => i !== index));
    // 如果正在编辑的是被删项之后的数据，索引要自适应或者取消编辑
    if (editingIndex === index) {
      cancelEdit();
    } else if (editingIndex !== null && editingIndex > index) {
      setEditingIndex(editingIndex - 1);
    }
  };

  // --- 提交接口逻辑 ---
  const checkAndSave = async (e: React.FormEvent) => {
    e.preventDefault();

    const addrError = getServerAddrValidationError(serverAddr);
    if (addrError) {
      toast.error(addrError);
      return;
    }
    const normalizedServerAddr = normalizeServerAddr(serverAddr);
    if (!normalizedServerAddr) {
      toast.error('请填写有效的 Client 连接地址');
      return;
    }
    setServerAddr(normalizedServerAddr);

    if (editingIndex !== null) {
      // 防止非键盘交互时触碰外层提交按钮
      toast.error("部分规则处于编辑状态，请先保存或取消后再整体提交！");
      return;
    }

    setSaved(false);
    setChecking(true);

    try {
      // 调用检查接口时同样要清理内部的 _id 属性
      const cleanPorts = toPayloadPorts(portRanges);
      const result = await api.put<{ affected_tunnels: AffectedTunnel[] }>(
        '/api/admin/config?dry_run=true',
        { server_addr: normalizedServerAddr, allowed_ports: cleanPorts },
      );

      if (result.affected_tunnels && result.affected_tunnels.length > 0) {
        setAffectedTunnels(result.affected_tunnels);
        setShowConfirm(true);
      } else {
        await doSave();
      }
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : '检查配置失败，请检查网络或服务端日志';
      toast.error(message);
      console.error('检查配置失败', error);
    } finally {
      setChecking(false);
    }
  };

  const doSave = async () => {
    try {
      const normalizedServerAddr = normalizeServerAddr(serverAddr);
      if (!normalizedServerAddr) {
        toast.error('请填写有效的 Client 连接地址');
        return;
      }
      // 剔除内部使用的 _id 避免污染通过网络发送的 Payload 
      const cleanPorts = toPayloadPorts(portRanges);
      await updateConfig.mutateAsync({
        server_addr: normalizedServerAddr,
        allowed_ports: cleanPorts,
      });
      setServerAddr(normalizedServerAddr);
      setSaved(true);
      setShowConfirm(false);
      setAffectedTunnels([]);
      setTimeout(() => setSaved(false), 2000);
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : '保存配置失败，请重试';
      toast.error(message);
      console.error('保存配置失败', error);
    }
  };

  const statusLabel = (status: string) => {
    switch (status) {
      case 'active': return '运行中';
      case 'paused': return '已暂停';
      case 'stopped': return '已停止';
      default: return status;
    }
  };

  // 渲染正在编辑的行还是渲染正在新增的行都会共用一部分逻辑。
  // 为了美观，我们把 `portRanges` 和如果 `editingIndex === length` 的虚拟行拼在一起 map。
  const displayRanges: DisplayRangeRow[] = editingIndex === portRanges.length 
    ? [...portRanges, { isAdding: true }] 
    : portRanges;

  return (
    <>
      <form onSubmit={checkAndSave} className="rounded-xl border border-border/40 bg-card/60 overflow-hidden shadow-sm backdrop-blur-sm">
        {/* 行 1: 服务地址 */}
        <div className="grid grid-cols-[280px_1fr] border-b border-border/40">
          <div className="p-6 bg-muted/20">
            <h4 className="font-semibold text-foreground">Client 连接地址</h4>
            <p className="text-sm text-muted-foreground mt-1">Client 建立控制通道和数据通道时使用的服务端地址，支持 HTTP(S) 与 WS(S)。</p>
          </div>
          <div className="p-6">
            <div className="max-w-md">
              <Input
                value={serverAddr}
                onChange={(e) => setServerAddr(e.target.value)}
                placeholder={SERVER_ADDR_PLACEHOLDER}
                className="w-full"
              />
              <p className="text-xs text-muted-foreground mt-2">{SERVER_ADDR_HELP_TEXT}</p>
            </div>
          </div>
        </div>

        {/* 行 2: 端口范围 */}
        <div className="grid grid-cols-[280px_1fr] border-b border-border/40">
          <div className="p-6 bg-muted/20">
            <h4 className="font-semibold text-foreground">穿透端口范围</h4>
            <p className="text-sm text-muted-foreground mt-1">管控放行区间的流量端口。为了避免冲突覆盖，仅支持每次处理一条规则的编辑与新增。</p>
          </div>
          <div className="p-6 flex flex-col items-start min-h-[160px]">
            {displayRanges.length === 0 ? (
              <div className="text-sm text-muted-foreground p-3 border border-red-500/30 bg-red-500/10 text-red-600 rounded-md inline-flex items-center gap-2 mb-4 w-full max-w-2xl">
                <ShieldAlert className="w-4 h-4" />
                警告：未设置任何过滤规则，所有的端口均开放穿透许可。
              </div>
            ) : (
              <div className="bg-card border border-border/60 rounded-md overflow-hidden mb-4 w-full max-w-2xl">
                <table className="w-full text-sm">
                  <thead className="bg-muted/40 border-b border-border/50 text-left text-muted-foreground">
                    <tr>
                      <th className="py-2.5 px-4 font-medium w-[30%]">起始端口</th>
                      <th className="py-2.5 px-4 font-medium w-[30%]">结束端口</th>
                      <th className="py-2.5 px-4 text-right w-[40%]">操作</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border/30 relative">
                    <AnimatePresence initial={false}>
                      {displayRanges.map((range, index) => {
                        const isEditing = editingIndex === index;
                        const isAddingRow = 'isAdding' in range;
                        // 利用我们在数据生成时注入的唯一本地 _id
                        const rowKey = isAddingRow ? 'new-pending-row' : range._id;

                        return (
                          <motion.tr 
                            key={rowKey}
                            layout
                            initial={{ opacity: 0, x: -10 }}
                            animate={{ opacity: 1, x: 0 }}
                            exit={{ opacity: 0, x: 10, filter: 'blur(2px)' }}
                            transition={{ duration: 0.2 }}
                            className="hover:bg-muted/30 transition-colors h-14"
                          >
                            {isEditing ? (
                              <>
                                <td className="py-2 px-4">
                                  <Input 
                                    type="number" min={1} max={65535}
                                    value={editForm.start} 
                                    onChange={e => setEditForm({...editForm, start: e.target.value === '' ? '' : Number.parseInt(e.target.value) || ''})}
                                    onKeyDown={handleInputKeyDown}
                                    className="h-8 shadow-sm w-32" 
                                  />
                                </td>
                                <td className="py-2 px-4 relative">
                                  <Input 
                                    type="number" min={1} max={65535}
                                    value={editForm.end} 
                                    onChange={e => setEditForm({...editForm, end: e.target.value === '' ? '' : Number.parseInt(e.target.value) || ''})}
                                    onKeyDown={handleInputKeyDown}
                                    className="h-8 shadow-sm w-32" 
                                  />
                                </td>
                                <td className="py-2 px-4 text-right flex items-center justify-end gap-1.5 h-full pt-3">
                                  <Button type="button" variant="ghost" size="sm" onClick={saveEdit} className="text-emerald-600 hover:text-emerald-700 hover:bg-emerald-500/10 h-8 px-2.5">
                                    <Check className="w-3.5 h-3.5 mr-1" /> 保存
                                  </Button>
                                  <Button type="button" variant="ghost" size="sm" onClick={cancelEdit} className="text-muted-foreground hover:bg-muted h-8 px-2.5">
                                    <X className="w-3.5 h-3.5 mr-1" /> 取消
                                  </Button>
                                </td>
                              </>
                            ) : (
                              !isAddingRow ? (
                                <>
                                  <td className="py-3 px-4 font-mono">{range.start}</td>
                                  <td className="py-3 px-4 font-mono">{range.end}</td>
                                  <td className="py-2 px-4 text-right flex items-center justify-end gap-1">
                                    <Button 
                                      type="button" 
                                      variant="ghost" 
                                      size="sm" 
                                      disabled={editingIndex !== null}
                                      onClick={() => startEdit(index)} 
                                      className="text-primary hover:text-primary hover:bg-primary/10 h-8 px-2.5"
                                    >
                                      <Edit2 className="w-3.5 h-3.5 mr-1.5" /> 编辑
                                    </Button>
                                    <Button 
                                      type="button" 
                                      variant="ghost" 
                                      size="sm"
                                      disabled={editingIndex !== null} 
                                      onClick={() => removePortRange(index)} 
                                      className="text-red-500 hover:text-red-600 hover:bg-red-500/10 h-8 px-2.5"
                                    >
                                      <Trash2 className="w-3.5 h-3.5 mr-1.5" /> 删除
                                    </Button>
                                  </td>
                                </>
                              ) : null
                            )}
                          </motion.tr>
                        );
                      })}
                    </AnimatePresence>
                  </tbody>
                </table>
              </div>
            )}
            
            <Button 
              type="button" 
              variant="outline" 
              size="sm" 
              onClick={startAdd} 
              disabled={editingIndex !== null}
              className="gap-2 shrink-0 border-dashed"
            >
              <Plus className="w-3.5 h-3.5" /> 声明新的端口防区
            </Button>
          </div>
        </div>

        {/* 底部操作区 */}
        <div className="p-6 flex justify-end bg-muted/10 items-center gap-4">
          <AnimatePresence>
            {saved && (
              <motion.div 
                initial={{ opacity: 0, x: 10 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: -10 }}
                transition={{ duration: 0.2 }}
                className="text-sm text-emerald-500 font-medium flex items-center gap-1.5"
              >
                <span className="w-2 h-2 rounded-full bg-emerald-500"></span> 已成功应用修改
              </motion.div>
            )}
          </AnimatePresence>
          <Button type="submit" disabled={updateConfig.isPending || checking || editingIndex !== null} className="min-w-32 shadow-sm">
            {checking ? '校验中...' : updateConfig.isPending ? '保存中...' : '提交全局配置'}
          </Button>
        </div>
      </form>

      {/* 受影响隧道二次确认弹窗 */}
      <AlertDialog open={showConfirm} onOpenChange={setShowConfirm}>
        <AlertDialogContent className="!max-w-lg w-[calc(100vw-2rem)]">
          <AlertDialogHeader>
            <AlertDialogMedia className="bg-amber-500/10">
              <AlertTriangle className="text-amber-500" />
            </AlertDialogMedia>
            <AlertDialogTitle>端口白名单变更影响提示</AlertDialogTitle>
            <AlertDialogDescription>
              以下 <span className="font-semibold text-foreground">{affectedTunnels.length}</span> 条现有隧道的端口不在新的白名单范围内，保存后这些隧道将被标记为异常并停止转发。
            </AlertDialogDescription>
          </AlertDialogHeader>

          {/* 受影响的隧道列表 */}
          <div className="max-h-60 overflow-auto rounded-lg border border-border/60 bg-muted/20">
            <table className="w-full text-sm min-w-[400px]">
              <thead>
                <tr className="border-b border-border/40 text-muted-foreground">
                  <th className="text-left py-2 px-3 font-medium">节点</th>
                  <th className="text-left py-2 px-3 font-medium">隧道</th>
                  <th className="text-right py-2 px-3 font-medium">端口</th>
                  <th className="text-right py-2 px-3 font-medium">状态</th>
                </tr>
              </thead>
              <tbody>
                {affectedTunnels.map((t, i) => (
                  <tr key={i} className="border-b border-border/20 last:border-0">
                    <td className="py-2 px-3 font-mono text-xs">{t.display_name || t.hostname || t.client_id.slice(0, 8)}</td>
                    <td className="py-2 px-3">{t.tunnel_name}</td>
                    <td className="py-2 px-3 text-right font-mono">{t.remote_port}</td>
                    <td className="py-2 px-3 text-right">
                      <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium ${
                        t.status === 'active'
                          ? 'bg-emerald-500/10 text-emerald-600'
                          : t.status === 'paused'
                          ? 'bg-amber-500/10 text-amber-600'
                          : 'bg-zinc-500/10 text-zinc-500'
                      }`}>
                        {statusLabel(t.status)}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <AlertDialogFooter>
            <AlertDialogCancel>返回修改</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={doSave}
              disabled={updateConfig.isPending}
            >
              <AlertTriangle className="w-3.5 h-3.5 mr-1.5" />
              {updateConfig.isPending ? '强行断开并保存...' : '确认强行保存'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
