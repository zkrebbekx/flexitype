import type { RequestOptions } from '../http.js'
import { segment } from '../http.js'
import type { ListOptions, Page } from '../pagination.js'
import type {
  AccountWithToken,
  EffectiveAccount,
  FieldPermissions,
  ParkedEnvelope,
  PurgeReport,
  Role,
  Scope,
  ServiceAccount,
  Tenant,
} from '../models.js'
import { itemsOf, pageOf, pageQuery, requestPart, Service } from './base.js'

/** The body of a service-account creation. */
export interface CreateServiceAccountInput {
  tenant_name: string
  /** Lowercase `[a-z0-9_-]`, 2 to 64 characters. */
  name: string
  /** Optional when roles are given. At least one scope or one role is required. */
  scopes?: Scope[]
  /** Role names in the same tenant. The account gets the union of their scopes. */
  roles?: string[]
  field_permissions?: FieldPermissions
}

/** The body of a role upsert. The write replaces the whole role. */
export interface UpsertRoleInput {
  tenant_name: string
  name: string
  description?: string
  scopes?: Scope[]
  field_permissions?: FieldPermissions
}

/** The filters the parked-outbox listing and redrive accept. */
export interface ParkedFilter {
  /** Narrow to one event type. */
  eventType?: string | undefined
  /** Narrow to one envelope. */
  id?: string | undefined
}

/**
 * Tenant and service-account provisioning, plus the operator recovery
 * surfaces. Every operation here needs the admin scope.
 *
 * A tenant is not a request parameter on the data endpoints: the credential
 * carries it. These endpoints name a tenant because they provision it.
 */
export class AdminService extends Service {
  /** Every tenant. */
  async listTenants(options: RequestOptions = {}): Promise<Tenant[]> {
    return itemsOf(await this.http.request<{ items?: Tenant[] }>('GET', '/tenants', {}, options))
  }

  /** Creates a tenant. */
  createTenant(name: string, options: RequestOptions = {}): Promise<Tenant> {
    return this.http.request<Tenant>('POST', '/tenants', { body: { name } }, options)
  }

  /** Suspends or resumes a tenant. A suspended tenant's requests are refused. */
  setTenantActive(name: string, active: boolean, options: RequestOptions = {}): Promise<Tenant> {
    return this.http.request<Tenant>('PATCH', `/tenants/${segment(name)}`, { body: { active } }, options)
  }

  /** A tenant's service accounts, as stored. Secrets are never returned. */
  async listServiceAccounts(tenantName: string, options: RequestOptions = {}): Promise<ServiceAccount[]> {
    return itemsOf(
      await this.http.request<{ items?: ServiceAccount[] }>(
        'GET',
        '/service-accounts',
        { query: { tenant_name: tenantName } },
        options,
      ),
    )
  }

  /**
   * Creates a service account. The token comes back once and is never
   * recoverable — store it before the response is discarded.
   */
  createServiceAccount(
    input: CreateServiceAccountInput,
    options: RequestOptions = {},
  ): Promise<AccountWithToken> {
    return this.http.request<AccountWithToken>('POST', '/service-accounts', { body: input }, options)
  }

  /** Issues a new token. The previous secret stops working immediately. */
  rotateServiceAccount(id: string, options: RequestOptions = {}): Promise<AccountWithToken> {
    return this.http.request<AccountWithToken>('POST', `/service-accounts/${segment(id)}/rotate`, {}, options)
  }

  /** Revokes an account. Its token stops working within the auth cache TTL. */
  revokeServiceAccount(id: string, options: RequestOptions = {}): Promise<void> {
    return this.http.request<void>('DELETE', `/service-accounts/${segment(id)}`, {}, options)
  }

  /**
   * What the account can actually do, after its roles are merged in. A
   * non-empty `unresolved_roles` is a fault: such an account is denied every
   * attribute.
   */
  effectiveAccount(id: string, options: RequestOptions = {}): Promise<EffectiveAccount> {
    return this.http.request<EffectiveAccount>('GET', `/service-accounts/${segment(id)}/effective`, {}, options)
  }

  /** Replaces an account's roles and its own per-attribute overrides. */
  assignRoles(
    id: string,
    input: { roles?: string[]; field_permissions?: FieldPermissions },
    options: RequestOptions = {},
  ): Promise<void> {
    return this.http.request<void>('PUT', `/service-accounts/${segment(id)}/roles`, { body: input }, options)
  }

  /** A tenant's roles. */
  async listRoles(tenantName: string, options: RequestOptions = {}): Promise<Role[]> {
    return itemsOf(
      await this.http.request<{ items?: Role[] }>('GET', '/roles', { query: { tenant_name: tenantName } }, options),
    )
  }

  /** Creates or replaces a role. A partial update is not offered by design. */
  upsertRole(input: UpsertRoleInput, options: RequestOptions = {}): Promise<Role> {
    return this.http.request<Role>('PUT', '/roles', { body: input }, options)
  }

  /** Deletes a role. It answers 409 while an account still names it. */
  deleteRole(tenantName: string, name: string, options: RequestOptions = {}): Promise<void> {
    return this.http.request<void>(
      'DELETE',
      `/roles/${segment(name)}`,
      { query: { tenant_name: tenantName } },
      options,
    )
  }

  /**
   * The outbox envelopes that exhausted their dispatch retry budget. A parked
   * envelope is a committed change no consumer has seen, so redrive it before
   * the parked retention deletes it.
   */
  async listParkedOutbox(options: ListOptions & ParkedFilter = {}): Promise<Page<ParkedEnvelope>> {
    return pageOf(
      await this.http.request<Page<ParkedEnvelope>>(
        'GET',
        '/admin/outbox/parked',
        { query: { ...pageQuery(options), event_type: options.eventType, id: options.id } },
        requestPart(options),
      ),
    )
  }

  /** Returns parked envelopes to the retry queue. Consumers must dedupe on the envelope id. */
  async redriveOutbox(options: RequestOptions & ParkedFilter = {}): Promise<number> {
    const body = await this.http.request<{ redriven?: number }>(
      'POST',
      '/admin/outbox/redrive',
      { query: { event_type: options.eventType, id: options.id } },
      requestPart(options),
    )
    return body.redriven ?? 0
  }

  /**
   * Hard-deletes the calling tenant's entity DATA: every value, revision, link
   * and search document, plus the backing media blobs. The schema, the unit
   * families, the saved views and the control plane survive. It is
   * irreversible and audited.
   */
  purgeTenant(options: RequestOptions = {}): Promise<PurgeReport> {
    return this.http.request<PurgeReport>('POST', '/admin/purge', {}, options)
  }
}
