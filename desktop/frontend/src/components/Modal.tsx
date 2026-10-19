import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { store } from "../store";

export const close = () => store.setUI({ dialog: undefined });

// Modal is a dialog the store opens (ui.dialog); closing it clears that.
export function Modal({
  title,
  description,
  children,
  onClose = close,
  className,
}: {
  title: string;
  description?: React.ReactNode;
  children: React.ReactNode;
  onClose?: () => void;
  className?: string;
}) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className={cn("sm:max-w-lg", className)} onOpenAutoFocus={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : <DialogDescription className="sr-only">{title}</DialogDescription>}
        </DialogHeader>
        {children}
      </DialogContent>
    </Dialog>
  );
}
