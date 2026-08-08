import type { RequestOptions } from '../http.js'
import { segment } from '../http.js'
import type { SchemaBundle, SchemaImportResult, SchemaTemplate, SchemaTemplateSummary } from '../models.js'
import { itemsOf, Service } from './base.js'

/**
 * Schema transfer: export a tenant's schema, import one, and apply a template.
 *
 * A bundle is keyed by internal name, never by id, so it moves between
 * instances. An import creates what is missing and skips what is present, in
 * dependency order, so re-running it is safe and completes a partial run. It
 * is not one transaction.
 */
export class SchemaService extends Service {
  /** The tenant's whole schema as a portable bundle. */
  export(options: RequestOptions = {}): Promise<SchemaBundle> {
    return this.http.request<SchemaBundle>('GET', '/schema/export', {}, options)
  }

  /** Applies a bundle. It reports what it created and what it skipped. */
  import(bundle: SchemaBundle, options: RequestOptions = {}): Promise<SchemaImportResult> {
    return this.http.request<SchemaImportResult>('POST', '/schema/import', { body: bundle }, options)
  }

  /** The starter schemas the service ships. */
  async templates(options: RequestOptions = {}): Promise<SchemaTemplateSummary[]> {
    return itemsOf(
      await this.http.request<{ items?: SchemaTemplateSummary[] }>('GET', '/schema/templates', {}, options),
    )
  }

  /** One template with its bundle. */
  template(name: string, options: RequestOptions = {}): Promise<SchemaTemplate> {
    return this.http.request<SchemaTemplate>('GET', `/schema/templates/${segment(name)}`, {}, options)
  }

  /** Imports a template's bundle into the tenant. */
  applyTemplate(name: string, options: RequestOptions = {}): Promise<SchemaImportResult> {
    return this.http.request<SchemaImportResult>('POST', `/schema/templates/${segment(name)}/apply`, {}, options)
  }
}
