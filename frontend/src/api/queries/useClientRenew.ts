import { useMutation, useQueryClient } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { keys } from '@/api/queryKeys';
import { markLocalInvalidate } from '@/api/invalidationTracker';

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } } as const;

export interface ClientRenewRequest {
  emails: string[];
  months: number;
  days: number;
  resetUsage: boolean;
}

/** Renews users by calendar months or days from the later of today and their expiry, optionally clearing usage. */
export function useClientRenew() {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: (req: ClientRenewRequest) =>
      HttpUtil.post('/panel/api/clients/renew', req, JSON_HEADERS),
    onSuccess: (msg) => {
      if (!msg?.success) return;
      markLocalInvalidate();
      void Promise.all([
        queryClient.invalidateQueries({ queryKey: keys.clients.root() }),
        queryClient.invalidateQueries({ queryKey: keys.inbounds.root() }),
        queryClient.invalidateQueries({ queryKey: keys.traffic.root() }),
      ]);
    },
  });
  return mutation.mutateAsync;
}
