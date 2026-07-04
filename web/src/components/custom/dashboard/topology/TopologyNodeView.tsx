import { Server as ServerIcon, Laptop } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/lib/utils';

import type { TopologyNode } from './topology-model';
import { LABEL_HALO, truncateLabel } from './topology-rendering';

export function TopologyNodeView({
  node,
  focused,
  tunnelCount,
  opacity,
  onClick,
  onHover,
}: {
  node: TopologyNode;
  focused: boolean;
  tunnelCount: number;
  opacity: number;
  onClick: () => void;
  onHover: (hovering: boolean) => void;
}) {
  const { t } = useTranslation();
  const isServer = node.kind === 'server';

  return (
    <g
      data-node-id={node.id}
      className="cursor-pointer transition-opacity duration-300"
      style={{ opacity }}
      onClick={(event) => {
        event.stopPropagation();
        onClick();
      }}
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      role="button"
      aria-label={isServer ? t('dashboard.topologyServer') : node.label}
      aria-pressed={focused}
    >
      {isServer ? (
        <>
          <circle r={34} className="fill-primary/15" filter="url(#topo-soft)" />
          <circle r={25} className="topology-pulse fill-none stroke-primary/40" strokeWidth={1} />
          <rect
            x={-21}
            y={-21}
            width={42}
            height={42}
            rx={13}
            fill="var(--color-card)"
            strokeWidth={1.5}
            className={cn('transition-colors', focused ? 'stroke-primary' : 'stroke-primary/60')}
          />
          {focused && (
            <rect
              x={-26}
              y={-26}
              width={52}
              height={52}
              rx={16}
              fill="none"
              strokeWidth={1.2}
              strokeDasharray="3 5"
              className="stroke-primary/50"
            />
          )}
          <ServerIcon x={-10} y={-10} width={20} height={20} strokeWidth={1.75} className="text-primary" />
          <text y={38} textAnchor="middle" className="fill-foreground text-[11px] font-medium" style={LABEL_HALO}>
            {t('dashboard.topologyServer')}
          </text>
        </>
      ) : (
        <>
          <circle
            r={25}
            className={node.online ? 'fill-emerald-500/12' : 'fill-muted-foreground/8'}
            filter="url(#topo-soft)"
          />
          <circle
            r={17}
            fill="var(--color-card)"
            strokeWidth={1.5}
            className={cn(
              'transition-colors',
              focused
                ? 'stroke-primary'
                : node.online
                  ? 'stroke-emerald-500/60'
                  : 'stroke-border',
            )}
          />
          {focused && (
            <circle r={23} fill="none" strokeWidth={1.2} strokeDasharray="3 5" className="stroke-primary/50" />
          )}
          <Laptop
            x={-8}
            y={-8}
            width={16}
            height={16}
            strokeWidth={1.75}
            className={node.online ? 'text-foreground/80' : 'text-muted-foreground/70'}
          />
          <circle
            cx={12}
            cy={-12}
            r={4}
            stroke="var(--color-background)"
            strokeWidth={1.5}
            className={node.online ? 'fill-emerald-500' : 'fill-muted-foreground/50'}
          />
          {tunnelCount > 0 && (
            <g transform="translate(14, 12)">
              <circle r={7} fill="var(--color-muted)" stroke="var(--color-border)" strokeWidth={1} />
              <text
                textAnchor="middle"
                dy={2.5}
                className="fill-muted-foreground font-mono text-[8px] font-medium"
              >
                {tunnelCount}
              </text>
            </g>
          )}
          <text
            y={32}
            textAnchor="middle"
            className={cn(
              'text-[10.5px] font-medium',
              node.online ? 'fill-foreground' : 'fill-muted-foreground',
            )}
            style={LABEL_HALO}
          >
            {truncateLabel(node.label, 20)}
          </text>
        </>
      )}
    </g>
  );
}
