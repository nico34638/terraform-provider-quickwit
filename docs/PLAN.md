# Provider Terraform pour Quickwit — plan

## Contexte
On veut gérer les index Quickwit (puis les sources) en Terraform. Une ressource à plat qui recopierait l'API ne marche pas : chaque field a des options qui dépendent de son type (`text`, `datetime`, `object`…), et les `object` sont récursifs.
Le provider incident.io découpe un objet parent en ressources enfants (`incident_catalog_type` + `incident_catalog_type_attribute`). Pour ça, il fait un read‑modify‑write de tout le schéma parent, protégé par un mutex par parent.

Ce pattern ne se transpose pas tel quel à Quickwit :
- `doc_mapping` est **un seul document**, envoyé en entier par `POST /api/v1/indexes` et `PUT /api/v1/indexes/{id}`. Le PUT remplace toute la config.
- À la création, l'index doit déjà contenir les fields, au minimum `timestamp_field` et les `tag_fields`.
- Chaque PUT qui modifie le mapping génère un nouveau `doc_mapping_uid`. Avec N ressources enfants, on ferait N PUT par apply.

**Choix retenu (validé)** : on garde l'idée « un objet Terraform typé par type de field », mais sous forme de **data sources typées** qui produisent du JSON, sur le modèle de `aws_iam_policy_document`. La ressource `quickwit_index` reste atomique et reçoit la liste des fields.

**Stack (validée)** : un repo Go séparé, `terraform-provider-quickwit`, avec `terraform-plugin-framework`. Le client HTTP est écrit à la main, parce que l'OpenAPI utoipa (`GET /openapi.json`) ne décrit pas les options des fields (`HashMap<String, Object>`) et qu'il lui manque `GET /indexes/{id}` et `GET /indexes/{id}/sources/{sid}`.

## Décisions de setup
- Repo : `/home/nuco/dev/go/terraform-provider-quickwit`, module `github.com/nico34638/terraform-provider-quickwit` (migration vers une org plus tard).
- Dev : `nix develop` (go, opentofu via `TF_ACC_TERRAFORM_PATH`, golangci-lint) + `docker compose up -d` (Quickwit `edge` sur :7280).

## Faits API utilisés (source : ce repo)
- Routes : `quickwit-serve/src/index_api/index_resource.rs` et `source_resource.rs`.
  - Create : `POST /indexes`. Get : `GET /indexes/{id}`, qui renvoie `IndexMetadata` avec `index_config` et `sources[]`. Update : `PUT /indexes/{id}`, sémantique PUT complète. Delete : `DELETE /indexes/{id}`.
  - Sources : `POST /indexes/{id}/sources`, puis `GET`, `PUT` et `DELETE` sur `/indexes/{id}/sources/{sid}`. Le `PUT` applique aussi `enabled`.
  - Il n'existe pas de route pour lister les sources : elles arrivent dans `IndexMetadata.sources`.
- Règles de mise à jour d'un index (`quickwit-config/src/index_config/serialize.rs::load_index_config_update`, `prepare_doc_mapping_update` dans `index_config/mod.rs`) :
  - `index_id` et `index_uri` sont immuables.
  - `timestamp_field` est immuable.
  - Les `tokenizers` ne peuvent qu'être ajoutés.
  - Si le mapping change, l'UID doit changer. Le serveur en génère un si on l'omet.
  - Omettre `retention` supprime la politique de rétention.
- Types de fields (`quickwit-doc-mapper/src/doc_mapper/field_mapping_entry.rs`) : `text`, `i64`, `u64`, `f64`, `bool`, `ip`, `datetime`, `bytes`, `json`, `object`, `concatenate`, `array<T>`. Chaque type a sa struct d'options (`QuickwitTextOptions`, `QuickwitNumericOptions`, `QuickwitDateTimeOptions`…).
- Sources (`quickwit-config/src/source_config/`) :
  - Seuls kafka, kinesis, pulsar, pubsub et file+sqs sont modifiables. `source_type` est immuable.
  - Immuables par type : kafka `topic` ; kinesis `stream_name` et `region`/`endpoint`.
  - Le serveur teste la connectivité de la source au create et à l'update.
  - IDs réservés : `_ingest-api-source`, `_ingest-cli-source`, `_ingest-source`.
  - Plusieurs pipelines (`num_pipelines > 1`) seulement pour kafka, pubsub et file+sqs.

## Phase 1 — Squelette du provider et index (fait)

### Provider
`provider "quickwit" { endpoint, headers (map, pour un proxy d'authentification), timeout }`. `endpoint` se rabat sur `QUICKWIT_ENDPOINT`, puis `http://localhost:7280`. Quickwit n'a pas d'authentification native.

### Data sources de fields (une par type)
`quickwit_field_text`, `_numeric` (avec `type = i64|u64|f64`), `_bool`, `_ip`, `_datetime`, `_bytes`, `_json`, `_object`, `_concatenate`, plus `quickwit_tokenizer`.
- Attributs communs (sauf `object` et `concatenate`, qui ont les leurs) : `name`, `description`, `array` (bool, qui produit `array<T>`), `stored`, `indexed`, `fast`.
- Attributs propres à chaque type. Exemples :
  - text : `tokenizer`, `record`, `fieldnorms`, `fast_normalizer`.
  - datetime : `input_formats`, `output_format`, `fast_precision`.
- `quickwit_field_object` prend `field_mappings = [<json>…]`, ce qui gère la récursion.
- Validations au plan, en reprenant les règles Rust :
  - `fast` est interdit sur `array<bytes>`.
  - Un `concatenate` doit avoir au moins un field ou `include_dynamic_fields`.
  - Un `object` ne peut pas être vide.
- Sortie : un attribut calculé `json`, **canonique**. Il contient tous les défauts explicités et ses clés sont triées, pour que l'aller‑retour serveur reste stable.
- Code partagé : un package `internal/mapping/` avec des structs Go typées, une par type de field, et une fonction `Canonicalize(json) -> json` qui applique les défauts.

### Ressource `quickwit_index`
- `index_id` : RequiresReplace.
- `index_uri` : Optional+Computed, RequiresReplace.
- Attribut imbriqué `doc_mapping` (attributs imbriqués plutôt que blocs, comme le recommande le framework) :
  - `mode`, `dynamic_mapping` (json).
  - `field_mappings` (liste de strings JSON).
  - `timestamp_field` : RequiresReplace.
  - `tag_fields`, `partition_key`, `max_num_partitions`, `store_source`, `index_field_presence`, `store_document_size`.
  - `tokenizers` (liste JSON).
  - `doc_mapping_uid` : Computed ; il reste connu au plan tant que le mapping ne change pas, et passe à « known after apply » sinon.
- Attributs imbriqués `indexing_settings` (avec `merge_policy` ; `resources.heap_size` est mis à plat en `heap_size`), `ingest_settings`, `search_settings` (`default_search_fields`) et `retention`.
- CRUD :
  - Create : POST avec un JSON `version: "0.9"`.
  - Read : GET.
  - Update : PUT de la config complète, reconstruite à partir du plan.
  - Delete : DELETE.
  - Import par `index_id`.
- Égalité sémantique : on implémente un type custom pour `field_mappings` (et pour `tokenizers`, `dynamic_mapping`, `heap_size`, `maturation_period`). Au Read, on normalise la valeur renvoyée par le serveur et celle du state (JSON canonique, tailles, durées). Si elles sont égales, on garde la valeur du state. Ça évite les faux diffs causés par les défauts, l'ordre des clés ou l'écriture des unités (`2GB` ≡ `2000000000`, `48h` ≡ `2days`).
- `ModifyPlan` détecte au plan ce que le serveur refuserait, plutôt que d'échouer à l'apply :
  - un tokenizer retiré ;
  - un `timestamp_field` absent de `field_mappings` ;
  - un `timestamp_field` qui n'est pas un datetime fast et single-valued ;
  - des `tag_fields` inconnus ou d'un type refusé ;
  - une `retention` sans `timestamp_field` ;
  - un `dynamic_mapping` hors mode `dynamic` ;
  - une option de `merge_policy` inconnue pour son type.

## Phase 2 — Sources (fait)
Une ressource par type de source : `quickwit_source_kafka`, `_kinesis`, `_pulsar`, `_pubsub`, `_sqs`. Ici le pattern incident s'applique naturellement, parce qu'une source est un objet API à part entière et qu'il n'y a pas de read‑modify‑write à faire.
- Attributs communs : `index_id` (RequiresReplace), `source_id` (RequiresReplace, refuse les IDs réservés), `num_pipelines` (limité à 1 pour kinesis et pulsar), `enabled`, `input_format`, `transform { script, timezone }`.
- Les attributs immuables propres à un type sont en RequiresReplace (kafka `topic`, kinesis `stream_name`, `region` et `endpoint`).
- Les secrets kafka (`client_params`) et pulsar (`authentication`) sont marqués Sensitive.
- Update : PUT complet.
- Read : `GET /indexes/{id}/sources/{sid}`. Un 404 (source ou index supprimé) retire la ressource du state.
- Import : `index_id:source_id`.
- Quickwit supprime les sources avec leur index : pour les recréer dans le même apply quand l'index est remplacé, on documente `lifecycle { replace_triggered_by = [quickwit_index.x] }`.

## Phase 3 — Data source et publication
- Data source `quickwit_index` en lecture seule (fait) : tous les réglages, défauts serveur compris, plus `create_timestamp` et la liste des sources.
- Reste à faire : publication sur le registry (goreleaser et signature GPG).

## Arborescence du repo
```
terraform-provider-quickwit/
  main.go
  internal/client/        # client HTTP: indexes, sources, erreurs {"message"}
  internal/mapping/       # structs typées, défauts, Canonicalize
  internal/provider/      # provider.go, index_*.go (ressource, data source),
                          # field_data_sources.go, source_*.go, semantic_types.go
  examples/ docs/ (tfplugindocs)
  e2e/                    # main.tf + run.sh : test de bout en bout sur un vrai cluster
  testdata/localstack/    # init SQS/Kinesis pour les tests d'acceptance
  docker-compose.yml      # quickwit/quickwit:edge ; profil `sources` : Kafka + LocalStack
```

## Vérification
- **Tests unitaires** sur `internal/mapping` : pour chaque type, comparer la sortie du data source au JSON attendu, vérifier les validations, et vérifier que la canonicalisation est idempotente.
- **Fixture d'aller‑retour** (`TF_ACC`, contre un Quickwit lancé en docker) : pour chaque type de field, faire un POST de l'index, puis un GET, et vérifier que `Canonicalize(GET) == Canonicalize(data source)`. C'est le principal risque de faux diffs.
- **Tests d'acceptance `resource.Test`** :
  - create → plan vide → update (ajout d'un field, changement de retention) → plan vide ;
  - changement de `timestamp_field` → replace ;
  - tokenizer retiré → refusé au plan, comme les autres validations ;
  - import ;
  - data source `quickwit_index` comparé à la ressource.
- Sources : tests d'acceptance kafka, SQS et kinesis contre le profil `sources` du `docker-compose.yml` (`QUICKWIT_TEST_SOURCES=1`) ; pubsub sans infra (pas de test de connectivité côté serveur). Pulsar : validations au plan et test unitaire des params seulement.
- Bout en bout : `e2e/run.sh` sur un vrai cluster (create → plan vide → update → import → destroy, vérifié via l'API).
- Contrôle manuel : `tofu apply` sur l'exemple `examples/logs`, puis `curl /api/v1/indexes/logs`.
