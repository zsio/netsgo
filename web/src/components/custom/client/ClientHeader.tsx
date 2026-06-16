import { useState, useRef, useEffect } from 'react';
import { motion, AnimatePresence } from 'motion/react';
import { Button } from '@/components/ui/button';
import { Pencil, Check, X, Loader2 } from 'lucide-react';
import { api } from '@/lib/api';
import { useQueryClient } from '@tanstack/react-query';
import type { Client } from '@/types';
import { formatUptime } from '@/lib/format';
import { getClientDisplayName } from '@/lib/client-utils';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';

interface ClientHeaderProps {
  client: Client;
}

const osLabels: Record<string, string> = {
  darwin: 'macOS',
  linux: 'Linux',
  windows: 'Windows',
};

export function ClientHeader({ client }: ClientHeaderProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();

  const [isEditing, setIsEditing] = useState(false);
  const [editValue, setEditValue] = useState('');
  const [isSaving, setIsSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const displayName = getClientDisplayName(client);

  const startEdit = () => {
    setEditValue(client.display_name || '');
    setSaveError(null);
    setIsEditing(true);
  };

  useEffect(() => {
    if (isEditing && inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [isEditing]);

  const cancelEdit = () => {
    setIsEditing(false);
    setEditValue('');
    setSaveError(null);
  };

  const saveDisplayName = async () => {
    setIsSaving(true);
    try {
      await api.put(`/api/clients/${encodeURIComponent(client.id)}/display-name`, {
        display_name: editValue.trim(),
      });
      queryClient.invalidateQueries({ queryKey: ['clients'] });
      setIsEditing(false);
      setSaveError(null);
    } catch (err) {
      const message = err instanceof Error ? err.message : t('clients.saveDisplayNameFailed');
      setSaveError(message);
      toast.error(message);
    } finally {
      setIsSaving(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      saveDisplayName();
    } else if (e.key === 'Escape') {
      cancelEdit();
    }
  };

  return (
    <div className="flex items-start justify-between min-w-0">
      <div className="min-w-0">
        <div className="flex items-center gap-3 mb-2">
          <div className="min-w-0">
            {/* 标题区域 — 切换 display / edit 模式 */}
            <div className="flex items-center gap-2 min-h-[36px]">
              <AnimatePresence mode="wait" initial={false}>
                {isEditing ? (
                  <motion.div
                    key="edit"
                    className="flex items-center gap-2 min-w-0"
                    initial={{ opacity: 0, y: -6, scale: 0.97 }}
                    animate={{ opacity: 1, y: 0, scale: 1 }}
                    exit={{ opacity: 0, y: 6, scale: 0.97 }}
                    transition={{ duration: 0.2, ease: [0.4, 0, 0.2, 1] }}
                  >
                    <motion.div
                      className="relative min-w-0"
                      initial={{ width: 120 }}
                      animate={{ width: 'auto' }}
                      transition={{ duration: 0.25 }}
                    >
                      <input
                        ref={inputRef}
                        type="text"
                        value={editValue}
                        onChange={(e) => setEditValue(e.target.value)}
                        onKeyDown={handleKeyDown}
                        placeholder={client.info.hostname}
                        className="text-2xl font-bold tracking-tight text-foreground bg-transparent border-b-2 border-primary/50 focus:border-primary outline-none px-0.5 py-0 w-[min(400px,70vw)] min-w-[120px] transition-colors duration-200"
                        disabled={isSaving}
                      />
                      {/* 底部高亮线动画 */}
                      <motion.div
                        className="absolute bottom-0 left-0 h-[2px] bg-primary"
                        initial={{ width: '0%' }}
                        animate={{ width: '100%' }}
                        transition={{ duration: 0.3, ease: 'easeOut', delay: 0.1 }}
                      />
                    </motion.div>

                    {/* 确认按钮 */}
                    <motion.div
                      initial={{ opacity: 0, scale: 0.5 }}
                      animate={{ opacity: 1, scale: 1 }}
                      transition={{ duration: 0.2, delay: 0.1 }}
                    >
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-emerald-500 hover:text-emerald-600 hover:bg-emerald-500/10 transition-colors"
                        onClick={saveDisplayName}
                        disabled={isSaving}
                      >
                        {isSaving ? (
                          <Loader2 className="h-4 w-4 animate-spin" />
                        ) : (
                          <Check className="h-4 w-4" />
                        )}
                      </Button>
                    </motion.div>

                    {/* 取消按钮 */}
                    <motion.div
                      initial={{ opacity: 0, scale: 0.5 }}
                      animate={{ opacity: 1, scale: 1 }}
                      transition={{ duration: 0.2, delay: 0.15 }}
                    >
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                        onClick={cancelEdit}
                        disabled={isSaving}
                      >
                        <X className="h-4 w-4" />
                      </Button>
                    </motion.div>
                  </motion.div>
                ) : (
                  <motion.div
                    key="display"
                    className="flex items-center gap-2 min-w-0"
                    initial={{ opacity: 0, y: 6, scale: 0.97 }}
                    animate={{ opacity: 1, y: 0, scale: 1 }}
                    exit={{ opacity: 0, y: -6, scale: 0.97 }}
                    transition={{ duration: 0.2, ease: [0.4, 0, 0.2, 1] }}
                  >
                    <h1 className="text-2xl font-bold tracking-tight text-foreground flex items-center gap-2 min-w-0 max-w-[min(520px,70vw)] truncate" title={displayName}>
                      {displayName}
                    </h1>
                    <motion.div whileHover={{ scale: 1.15 }} whileTap={{ scale: 0.9 }}>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-muted-foreground hover:text-foreground transition-colors"
                        onClick={startEdit}
                        title={t('clients.editDisplayName')}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                    </motion.div>
                  </motion.div>
                )}
              </AnimatePresence>
            </div>
            {saveError && (
              <p className="mt-1 text-xs font-medium text-destructive">
                {saveError}
              </p>
            )}

            {/* Metadata 行 */}
            <div className="text-sm text-muted-foreground flex items-center gap-2 mt-1 flex-wrap">
              <span className="font-mono bg-muted/50 px-1.5 py-0.5 rounded">{client.id.slice(0, 8)}</span>
              <span>•</span>
              <AnimatePresence mode="popLayout">
                {client.display_name && (
                  <motion.span
                    key="hostname-tag"
                    initial={{ opacity: 0, width: 0, marginRight: 0 }}
                    animate={{ opacity: 1, width: 'auto', marginRight: 0 }}
                    exit={{ opacity: 0, width: 0, marginRight: 0 }}
                    transition={{ duration: 0.25, ease: [0.4, 0, 0.2, 1] }}
                    className="inline-flex items-center gap-2 overflow-hidden"
                  >
                    <span className="text-xs" title={t('clients.machineName')}>{client.info.hostname}</span>
                    <span>•</span>
                  </motion.span>
                )}
              </AnimatePresence>
              <span>{osLabels[client.info.os] ?? client.info.os} / {client.info.arch}</span>
              <span>•</span>
              <span>{client.info.ip}</span>
              {client.stats?.process_uptime != null && client.stats.process_uptime > 0 ? (
                <>
                  <span>•</span>
                  <span>{t('clients.processRunning', { uptime: formatUptime(client.stats.process_uptime) })}</span>
                </>
              ) : client.stats?.uptime != null && (
                <>
                  <span>•</span>
                  <span>{t('clients.systemUptime', { uptime: formatUptime(client.stats.uptime) })}</span>
                </>
              )}
            </div>
          </div>
        </div>
      </div>

      <div />
    </div>
  );
}
