import { useRef, useState } from 'react';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';

export function DualTriggerCard({
  triggers,
  children,
  align = 'center',
  side,
  triggerClassName,
  contentClassName,
}: {
  triggers: React.ReactNode;
  children: React.ReactNode;
  align?: 'start' | 'center' | 'end';
  side?: 'top' | 'right' | 'bottom' | 'left';
  triggerClassName?: string;
  contentClassName?: string;
}) {
  const [isOpen, setIsOpen] = useState(false);
  const [isPinned, setIsPinned] = useState(false);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const onMouseEnter = () => {
    clearTimeout(timeoutRef.current);
    setIsOpen(true);
  };

  const onMouseLeave = () => {
    timeoutRef.current = setTimeout(() => {
      setIsOpen(false);
    }, 150);
  };

  const onClick = () => {
    setIsPinned((prev) => {
      const next = !prev;
      if (next) {
        setIsOpen(true);
      }
      return next;
    });
  };

  const onOpenChange = (open: boolean) => {
    if (!open) {
      setIsOpen(false);
      setIsPinned(false);
    } else {
      setIsOpen(true);
    }
  };

  return (
    <Popover open={isOpen || isPinned} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>
        <div
          className={triggerClassName}
          onClick={onClick}
          onMouseEnter={onMouseEnter}
          onMouseLeave={onMouseLeave}
        >
          {triggers}
        </div>
      </PopoverTrigger>
      <PopoverContent
        align={align}
        side={side}
        className={cn('w-auto p-3 outline-none', contentClassName)}
        onMouseEnter={onMouseEnter}
        onMouseLeave={onMouseLeave}
      >
        {children}
      </PopoverContent>
    </Popover>
  );
}
