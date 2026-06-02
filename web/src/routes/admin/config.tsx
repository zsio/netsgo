import { useState } from 'react';
import { createRoute } from '@tanstack/react-router';
import { useTranslation, Trans } from 'react-i18next';
import { adminRoute } from '../admin';
import { ApiError } from '@/lib/api';
import { useAdminConfig, useUpdateAdminConfig } from '@/hooks/use-admin-config';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Plus, AlertTriangle, ShieldAlert, Edit2, Check, X, Trash2, HelpCircle } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { TableActionIconButton } from '@/components/custom/common/TableActionIconButton';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader,
  AlertDialogMedia, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { api } from '@/lib/api';
import { getServerAddrValidationIssue, normalizeServerAddr, SERVER_ADDR_HELP_TEXT, SERVER_ADDR_PLACEHOLDER } from '@/lib/server-address';
import { createLocalId } from '@/lib/utils';
import { resolveTunnelStatus } from '@/lib/tunnel-model';
import toast from 'react-hot-toast';
import type {
  AdminConfig,
  AdminConfigUpdateResponse,
  AffectedTunnel,
  PortRange,
} from '@/types';

export const adminConfigRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/config',
  component: AdminConfigPage,
});

type LocalPortRange = PortRange & { _id: string };
type DisplayRangeRow = LocalPortRange | { isAdding: true };

function AdminConfigPage() {
  const { data: config, isLoading } = useAdminConfig();
  const { t } = useTranslation();

  return (
    <div className="flex flex-col gap-6 w-full pb-10">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold tracking-tight">{t('admin.configTitle')}</h2>
          <p className="text-sm text-muted-foreground mt-1">
            {t('admin.configDescription')}
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

function AdminConfigForm({ initialConfig }: { initialConfig: AdminConfig }) {
  const { t } = useTranslation();
  const updateConfig = useUpdateAdminConfig();
  const [serverAddr, setServerAddr] = useState(initialConfig.server_addr || '');
  const initialServerAddr = (initialConfig.server_addr || '').trim();
  const initialServerAddrIsLegacy = initialServerAddr !== '' && getServerAddrValidationIssue(initialServerAddr) !== null;
  const serverAddrLocked = initialConfig.server_addr_locked;
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
  const [conflictingHTTPTunnels, setConflictingHTTPTunnels] = useState<string[]>([]);
  const [pendingServerAddr, setPendingServerAddr] = useState<string | null>(null);
  const [showConfirm, setShowConfirm] = useState(false);

  const toPayloadPorts = (ranges: LocalPortRange[]): PortRange[] =>
    ranges.map((range) => ({ start: range.start, end: range.end }));

  const resolveServerAddrForSubmit = () => {
    const trimmedServerAddr = serverAddr.trim();
    const isUnchangedLegacy = initialServerAddrIsLegacy && trimmedServerAddr === initialServerAddr;

    if (isUnchangedLegacy) {
      return {
        value: initialServerAddr,
        shouldUpdateInput: false,
      };
    }

    const addrIssue = getServerAddrValidationIssue(serverAddr);
    if (addrIssue) {
      toast.error(t(`serverAddress.${addrIssue.code}`, { defaultValue: addrIssue.message }));
      return null;
    }
    const normalizedServerAddr = normalizeServerAddr(serverAddr);
    if (!normalizedServerAddr) {
      toast.error(t('admin.invalidClientAddress'));
      return null;
    }

    return {
      value: normalizedServerAddr,
      shouldUpdateInput: normalizedServerAddr !== serverAddr,
    };
  };

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
      toast.error(t('admin.invalidPortRangeFields'));
      return;
    }
    if (startVal > endVal) {
      toast.error(t('admin.startPortAfterEnd'));
      return;
    }
    // 冲突重叠校验
    const isOverlap = portRanges.some((range, i) => {
      if (i === editingIndex) return false;
      return Math.max(startVal, range.start) <= Math.min(endVal, range.end);
    });

    if (isOverlap) {
      toast.error(t('admin.overlappingPortRange'));
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

    const resolvedServerAddr = resolveServerAddrForSubmit();
    if (!resolvedServerAddr) {
      return;
    }
    if (resolvedServerAddr.shouldUpdateInput) {
      setServerAddr(resolvedServerAddr.value);
    }

    if (editingIndex !== null) {
      // 防止非键盘交互时触碰外层提交按钮
      toast.error(t('admin.editingRulesActive'));
      return;
    }

    setSaved(false);
    setChecking(true);

    try {
      // 调用检查接口时同样要清理内部的 _id 属性
      const cleanPorts = toPayloadPorts(portRanges);
      setPendingServerAddr(resolvedServerAddr.value);
      const result = await api.put<AdminConfigUpdateResponse>(
        '/api/admin/config?dry_run=true',
        { server_addr: resolvedServerAddr.value, allowed_ports: cleanPorts },
      );

      const conflicts = result.conflicting_http_tunnels ?? [];
      setConflictingHTTPTunnels(conflicts);
      if (conflicts.length > 0) {
        setAffectedTunnels([]);
        setPendingServerAddr(null);
        toast.error(t('admin.serverAddrConflictToast'));
        return;
      }

      if (result.affected_tunnels && result.affected_tunnels.length > 0) {
        setAffectedTunnels(result.affected_tunnels);
        setShowConfirm(true);
      } else {
        await doSave(resolvedServerAddr.value);
      }
    } catch (error: unknown) {
      setPendingServerAddr(null);
      if (error instanceof ApiError) {
        const body = error.body as AdminConfigUpdateResponse | undefined;
        setConflictingHTTPTunnels(body?.conflicting_http_tunnels ?? []);
      }
      const message = error instanceof Error ? error.message : t('admin.configCheckFailed');
      toast.error(message);
      console.error('config check failed', error);
    } finally {
      setChecking(false);
    }
  };

  const doSave = async (serverAddrToSave?: string) => {
    try {
      const resolvedServerAddr = serverAddrToSave
        ? { value: serverAddrToSave, shouldUpdateInput: false }
        : resolveServerAddrForSubmit();
      if (!resolvedServerAddr) {
        return;
      }
      // 剔除内部使用的 _id 避免污染通过网络发送的 Payload 
      const cleanPorts = toPayloadPorts(portRanges);
      await updateConfig.mutateAsync({
        server_addr: resolvedServerAddr.value,
        allowed_ports: cleanPorts,
      });
      setServerAddr(resolvedServerAddr.value);
      setConflictingHTTPTunnels([]);
      setSaved(true);
      setShowConfirm(false);
      setAffectedTunnels([]);
      setPendingServerAddr(null);
      setTimeout(() => setSaved(false), 2000);
    } catch (error: unknown) {
      if (error instanceof ApiError) {
        const body = error.body as AdminConfigUpdateResponse | undefined;
        setConflictingHTTPTunnels(body?.conflicting_http_tunnels ?? []);
      }
      const message = error instanceof Error ? error.message : t('admin.configSaveFailed');
      toast.error(message);
      console.error('config save failed', error);
    }
  };

  const affectedTunnelStatus = (tunnel: AffectedTunnel) =>
    resolveTunnelStatus({
      desired_state: tunnel.desired_state,
      runtime_state: tunnel.runtime_state,
      error: tunnel.error,
    }, tunnel.runtime_state !== 'offline');

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
            <h4 className="font-semibold text-foreground">{t('admin.defaultServerAddress')}</h4>
            <p className="text-sm text-muted-foreground mt-1">{t('admin.defaultServerAddressHelp')}</p>
          </div>
          <div className="p-6">
            <div className="max-w-md">
              <div className="flex items-center justify-between gap-2 mb-2">
                <span className="text-sm font-medium text-foreground">server_addr</span>
                {serverAddrLocked ? (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <button
                        type="button"
                        aria-label={t('admin.serverAddrLockedLabel')}
                        className="inline-flex h-6 w-6 items-center justify-center rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors"
                      >
                        <HelpCircle className="h-4 w-4" />
                      </button>
                    </TooltipTrigger>
                    <TooltipContent side="top" className="max-w-[320px]">
                      <p>{t('admin.serverAddrLockedTooltip')}</p>
                    </TooltipContent>
                  </Tooltip>
                ) : null}
              </div>
              <Input
                value={serverAddr}
                onChange={(e) => {
                  setServerAddr(e.target.value);
                  setConflictingHTTPTunnels([]);
                }}
                placeholder={SERVER_ADDR_PLACEHOLDER}
                className="w-full"
                disabled={serverAddrLocked}
              />
              <p className="text-xs text-muted-foreground mt-2">{SERVER_ADDR_HELP_TEXT}</p>
              {initialServerAddrIsLegacy && serverAddr.trim() === initialServerAddr && (
                <p className="text-xs text-amber-600 mt-2">
                  {t('admin.legacyServerAddrWarning')}
                </p>
              )}

              {conflictingHTTPTunnels.length > 0 && (
                <div className="rounded-lg border border-destructive/30 bg-destructive/8 p-3 text-sm">
                  <div className="flex items-start gap-2 text-destructive">
                    <AlertTriangle className="w-4 h-4 shrink-0 mt-0.5" />
                    <div className="flex flex-col gap-1">
                      <p className="font-medium">{t('admin.serverAddrConflictTitle')}</p>
                      <p className="text-xs text-destructive/80">
                        {t('admin.serverAddrConflictHelp')}
                      </p>
                      <div className="flex flex-wrap gap-2 mt-1">
                        {conflictingHTTPTunnels.map((tunnel) => (
                          <code
                            key={tunnel}
                            className="rounded bg-background/80 px-2 py-1 text-[11px] text-foreground"
                          >
                            {tunnel}
                          </code>
                        ))}
                      </div>
                    </div>
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>

        {/* 行 2: 端口范围 */}
        <div className="grid grid-cols-[280px_1fr] border-b border-border/40">
          <div className="p-6 bg-muted/20">
            <h4 className="font-semibold text-foreground">{t('admin.portRanges')}</h4>
            <p className="text-sm text-muted-foreground mt-1">{t('admin.portRangesHelp')}</p>
          </div>
          <div className="p-6 flex flex-col items-start min-h-[160px]">
            {displayRanges.length === 0 ? (
              <div className="text-sm text-muted-foreground p-3 border border-red-500/30 bg-red-500/10 text-red-600 rounded-md inline-flex items-center gap-2 mb-4 w-full max-w-2xl">
                <ShieldAlert className="w-4 h-4" />
                {t('admin.openPortsWarning')}
              </div>
            ) : (
              <div className="bg-card border border-border/60 rounded-md overflow-hidden mb-4 w-full max-w-2xl">
                <table className="w-full text-sm">
                  <thead className="bg-muted/40 border-b border-border/50 text-left text-muted-foreground">
                    <tr>
                      <th className="py-2.5 px-4 font-medium w-[30%]">{t('admin.startPort')}</th>
                      <th className="py-2.5 px-4 font-medium w-[30%]">{t('admin.endPort')}</th>
                      <th className="py-2.5 px-4 text-right w-[40%]">{t('admin.actions')}</th>
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
                                  <TableActionIconButton type="button" label={t('common.save')} tone="success" onClick={saveEdit}>
                                    <Check className="w-3.5 h-3.5" />
                                  </TableActionIconButton>
                                  <TableActionIconButton type="button" label={t('common.cancel')} tone="neutral" onClick={cancelEdit}>
                                    <X className="w-3.5 h-3.5" />
                                  </TableActionIconButton>
                                </td>
                              </>
                            ) : (
                              !isAddingRow ? (
                                <>
                                  <td className="py-3 px-4 font-mono">{range.start}</td>
                                  <td className="py-3 px-4 font-mono">{range.end}</td>
                                  <td className="py-2 px-4 text-right flex items-center justify-end gap-1">
                                    <TableActionIconButton
                                      type="button"
                                      label={t('common.edit')}
                                      tone="primary"
                                      disabled={editingIndex !== null}
                                      onClick={() => startEdit(index)}
                                    >
                                      <Edit2 className="w-3.5 h-3.5" />
                                    </TableActionIconButton>
                                    <TableActionIconButton
                                      type="button"
                                      label={t('common.delete')}
                                      tone="destructive"
                                      disabled={editingIndex !== null}
                                      onClick={() => removePortRange(index)}
                                    >
                                      <Trash2 className="w-3.5 h-3.5" />
                                    </TableActionIconButton>
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
              <Plus className="w-3.5 h-3.5" /> {t('admin.addPortRange')}
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
                <span className="w-2 h-2 rounded-full bg-emerald-500"></span> {t('admin.configSaved')}
              </motion.div>
            )}
          </AnimatePresence>
          <Button type="submit" disabled={updateConfig.isPending || checking || editingIndex !== null} className="min-w-32 shadow-sm">
            {checking ? t('common.checking') : updateConfig.isPending ? t('common.saving') : t('admin.submitGlobalConfig')}
          </Button>
        </div>
      </form>

      {/* 受影响隧道二次确认弹窗 */}
      <AlertDialog open={showConfirm} onOpenChange={(open) => {
        setShowConfirm(open);
        if (!open) {
          setAffectedTunnels([]);
          setPendingServerAddr(null);
        }
      }}>
        <AlertDialogContent className="!max-w-lg w-[calc(100vw-2rem)]">
          <AlertDialogHeader>
            <AlertDialogMedia className="bg-amber-500/10">
              <AlertTriangle className="text-amber-500" />
            </AlertDialogMedia>
            <AlertDialogTitle>{t('admin.affectedPortsTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              <Trans
                i18nKey="admin.affectedPortsDescription"
                values={{ count: affectedTunnels.length }}
                components={{ strong: <span className="font-semibold text-foreground" /> }}
              />
            </AlertDialogDescription>
          </AlertDialogHeader>

          {/* 受影响的隧道列表 */}
          <div className="max-h-60 overflow-auto rounded-lg border border-border/60 bg-muted/20">
            <table className="w-full text-sm min-w-[400px]">
              <thead>
                <tr className="border-b border-border/40 text-muted-foreground">
                  <th className="text-left py-2 px-3 font-medium">{t('admin.node')}</th>
                  <th className="text-left py-2 px-3 font-medium">{t('tunnels.tunnel')}</th>
                  <th className="text-right py-2 px-3 font-medium">{t('tunnels.publicPort')}</th>
                  <th className="text-right py-2 px-3 font-medium">{t('tunnels.status')}</th>
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
                        affectedTunnelStatus(t).key === 'exposed'
                          ? 'bg-emerald-500/10 text-emerald-600'
                          : affectedTunnelStatus(t).key === 'stopped'
                          ? 'bg-amber-500/10 text-amber-600'
                          : affectedTunnelStatus(t).key === 'error'
                          ? 'bg-destructive/10 text-destructive'
                          : 'bg-zinc-500/10 text-zinc-500'
                      }`}>
                        {affectedTunnelStatus(t).label}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <AlertDialogFooter>
            <AlertDialogCancel>{t('admin.returnToEdit')}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => doSave(pendingServerAddr ?? undefined)}
              disabled={updateConfig.isPending}
            >
              <AlertTriangle className="w-3.5 h-3.5 mr-1.5" />
              {updateConfig.isPending ? t('admin.forceSaving') : t('admin.forceSave')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
