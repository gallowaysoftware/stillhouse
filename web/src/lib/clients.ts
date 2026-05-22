import { createClient } from "@connectrpc/connect";

import { AuthService } from "@/gen/stillhouse/v1/auth_pb";
import { TenantService } from "@/gen/stillhouse/v1/tenant_pb";
import { UserService } from "@/gen/stillhouse/v1/user_pb";

import { transport } from "./transport";

export const authClient = createClient(AuthService, transport);
export const tenantClient = createClient(TenantService, transport);
export const userClient = createClient(UserService, transport);
