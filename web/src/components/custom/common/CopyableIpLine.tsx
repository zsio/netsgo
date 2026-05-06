import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Check, Copy } from 'lucide-react';

interface CopyableIpLineProps {
  icon: ReactNode;
  value: string;
  title: string;
  primary?: boolean;
}

function canCopy(value: string) {
  return value.trim() !== '' && value !== '-';
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }

  const textarea = document.createElement('textarea');
  textarea.value = value;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand('copy');
  document.body.removeChild(textarea);
}

export function CopyableIpLine({ icon, value, title, primary = false }: CopyableIpLineProps) {
  const [copied, setCopied] = useState(false);
  const copyTimerRef = useRef<number | null>(null);
  const copyable = canCopy(value);

  useEffect(() => () => {
    if (copyTimerRef.current !== null) {
      window.clearTimeout(copyTimerRef.current);
    }
  }, []);

  const resetCopyTimer = () => {
    if (copyTimerRef.current !== null) {
      window.clearTimeout(copyTimerRef.current);
    }
    copyTimerRef.current = window.setTimeout(() => {
      setCopied(false);
      copyTimerRef.current = null;
    }, 1200);
  };

  const handleCopy = async () => {
    if (!copyable) return;

    try {
      await copyText(value);
      setCopied(true);
      resetCopyTimer();
    } catch {
      setCopied(false);
    }
  };

  return (
    <div className={primary ? 'font-medium text-sm text-foreground min-w-0' : 'text-xs text-muted-foreground min-w-0'}>
      <span className="inline-flex items-center gap-1.5 min-w-0 group/ip">
        <span className="shrink-0 text-muted-foreground" title={title} aria-label={title}>{icon}</span>
        <span className="font-mono break-all">{value}</span>
        {copyable && (
          <button
            type="button"
            className="shrink-0 opacity-0 group-hover/ip:opacity-100 focus-visible:opacity-100 transition-opacity text-muted-foreground hover:text-foreground rounded-sm"
            title={`复制${title}`}
            aria-label={`复制${title}`}
            onClick={handleCopy}
          >
            {copied ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
          </button>
        )}
      </span>
    </div>
  );
}
