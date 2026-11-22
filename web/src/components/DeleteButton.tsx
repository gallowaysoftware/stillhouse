import { useConfirm } from "@/components/ConfirmDialog";
import { useToast } from "@/components/Toast";
import { writeErrorText } from "@/lib/errors";

/** Removes one recorded entry, after asking. */
export function DeleteButton({ title, body, onDelete }: { title: string; body: string; onDelete: () => Promise<unknown> }) {
  const confirm = useConfirm();
  const toast = useToast();
  return (
    <button
      type="button"
      className="rounded px-2 py-2 text-xs text-danger-fg hover:bg-surface-3"
      onClick={async () => {
        if (!(await confirm({ title, body, confirmLabel: "Delete", tone: "danger" }))) return;
        try {
          await onDelete();
        } catch (e) {
          toast("danger", writeErrorText(e));
        }
      }}
    >
      Delete
    </button>
  );
}
