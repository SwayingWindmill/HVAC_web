#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/stat.h>
#include <time.h>

#include <cjson/cJSON.h>
#include <openssl/asn1.h>
#include <openssl/bio.h>
#include <openssl/err.h>
#include <openssl/evp.h>
#include <openssl/pem.h>
#include <openssl/x509.h>

#include <mosquitto.h>
#include <mosquitto_broker.h>
#include <mosquitto_plugin.h>

#if LIBMOSQUITTO_VERSION_NUMBER < 2001000
#define MOSQ_EVT_CONNECT 11
#define MOSQ_EVT_CLIENT_OFFLINE 28

struct mosquitto_evt_connect {
	void *future;
	struct mosquitto *client;
	void *future2[4];
};

struct mosquitto_evt_client_offline {
	void *future;
	struct mosquitto *client;
	int reason;
	void *future2[4];
};
#endif

#define DEFAULT_CAFILE "/mosquitto/config/gateway-ca.pem"
#define DEFAULT_CRLFILE "/mosquitto/config/crl/gateway-ca.crl.pem"
#define DEFAULT_CONTROL_USER "mqtt-telemetry-adapter"
#define CONTROL_TOPIC "$CONTROL/hvac-crl/v1"
#define MAX_CONTROL_PAYLOAD_BYTES 65536

#ifdef MOSQUITTO_PLUGIN_DECLARE_VERSION
MOSQUITTO_PLUGIN_DECLARE_VERSION(5);
#else
int mosquitto_plugin_version(int supported_version_count, const int *supported_versions)
{
	for(int i = 0; i < supported_version_count; i++){
		if(supported_versions[i] == 5){
			return 5;
		}
	}
	return -1;
}
#endif

struct ca_entry {
	X509 *cert;
	X509_NAME *subject;
	struct ca_entry *next;
};

struct client_entry {
	const struct mosquitto *client_ptr;
	char *clientid;
	char *username;
	X509_NAME *issuer;
	ASN1_INTEGER *serial;
	char fingerprint_sha256[65];
	struct client_entry *next;
};

struct crl_plugin_ctx {
	mosquitto_plugin_id_t *plugin_id;
	char *cafile;
	char *crlfile;
	char *control_user;
	time_t poll_seconds;
	time_t last_poll_s;

	struct ca_entry *cas;
	X509_CRL *active_crl;
	X509_NAME *active_crl_issuer;

	bool has_last_stat;
	time_t last_mtime_sec;
	long last_mtime_nsec;
	ino_t last_ino;
	off_t last_size;
	bool missing_logged;

	bool registered_connect;
	bool registered_offline;
	bool registered_reload;
	bool registered_tick;

	const char *will_grace_clientid;
	struct client_entry *clients;
};

static char *dup_or_null(const char *s)
{
	if(!s){
		return NULL;
	}
	return strdup(s);
}

static void free_ca_list(struct crl_plugin_ctx *ctx)
{
	struct ca_entry *cur = ctx->cas;
	while(cur){
		struct ca_entry *next = cur->next;
		X509_free(cur->cert);
		free(cur);
		cur = next;
	}
	ctx->cas = NULL;
}

static void free_client_entry(struct client_entry *entry)
{
	if(!entry){
		return;
	}
	free(entry->clientid);
	free(entry->username);
	X509_NAME_free(entry->issuer);
	ASN1_INTEGER_free(entry->serial);
	free(entry);
}

static void free_all_clients(struct crl_plugin_ctx *ctx)
{
	struct client_entry *cur = ctx->clients;
	while(cur){
		struct client_entry *next = cur->next;
		free_client_entry(cur);
		cur = next;
	}
	ctx->clients = NULL;
}

static bool load_ca_bundle(struct crl_plugin_ctx *ctx)
{
	BIO *bio = BIO_new_file(ctx->cafile, "rb");
	if(!bio){
		mosquitto_log_printf(MOSQ_LOG_ERR,
				"hvac_crl: unable to open cafile %s (%s)",
				ctx->cafile, strerror(errno));
		return false;
	}

	struct ca_entry *new_head = NULL;
	int count = 0;
	for(;;){
		ERR_clear_error();
		X509 *cert = PEM_read_bio_X509(bio, NULL, NULL, NULL);
		if(!cert){
			break;
		}
		struct ca_entry *node = calloc(1, sizeof(*node));
		if(!node){
			X509_free(cert);
			BIO_free(bio);
			while(new_head){
				struct ca_entry *tmp = new_head->next;
				X509_free(new_head->cert);
				free(new_head);
				new_head = tmp;
			}
			return false;
		}
		node->cert = cert;
		node->subject = X509_get_subject_name(cert);
		node->next = new_head;
		new_head = node;
		count++;
	}
	BIO_free(bio);

	if(count == 0){
		mosquitto_log_printf(MOSQ_LOG_ERR,
				"hvac_crl: no CA certificates parsed from %s",
				ctx->cafile);
		return false;
	}

	free_ca_list(ctx);
	ctx->cas = new_head;
	mosquitto_log_printf(MOSQ_LOG_INFO,
			"hvac_crl: loaded %d CA certificate(s) from %s",
			count, ctx->cafile);
	return true;
}

static bool verify_crl_against_cas(struct crl_plugin_ctx *ctx, X509_CRL *crl)
{
	const X509_NAME *issuer = X509_CRL_get_issuer(crl);
	if(!issuer){
		return false;
	}
	for(struct ca_entry *ca = ctx->cas; ca != NULL; ca = ca->next){
		if(!ca->subject || X509_NAME_cmp(ca->subject, issuer) != 0){
			continue;
		}
		EVP_PKEY *pubkey = X509_get0_pubkey(ca->cert);
		if(pubkey && X509_CRL_verify(crl, pubkey) == 1){
			return true;
		}
	}
	return false;
}

static void record_stat(struct crl_plugin_ctx *ctx, const struct stat *st)
{
	ctx->has_last_stat = true;
	ctx->last_mtime_sec = st->st_mtim.tv_sec;
	ctx->last_mtime_nsec = st->st_mtim.tv_nsec;
	ctx->last_ino = st->st_ino;
	ctx->last_size = st->st_size;
}

static bool reload_crl_if_changed(struct crl_plugin_ctx *ctx, bool force)
{
	struct stat st;
	if(stat(ctx->crlfile, &st) != 0){
		if(errno == ENOENT){
			if(ctx->active_crl && !ctx->missing_logged){
				mosquitto_log_printf(MOSQ_LOG_WARNING,
						"hvac_crl: crlfile %s disappeared; preserving last verified CRL fail-closed",
						ctx->crlfile);
				ctx->missing_logged = true;
			}else if(!ctx->active_crl && !ctx->missing_logged){
				mosquitto_log_printf(MOSQ_LOG_INFO,
						"hvac_crl: crlfile %s not present yet; waiting for Connectivity export",
						ctx->crlfile);
				ctx->missing_logged = true;
			}
		}else{
			mosquitto_log_printf(MOSQ_LOG_WARNING,
					"hvac_crl: stat(%s) failed: %s",
					ctx->crlfile, strerror(errno));
		}
		return false;
	}

	ctx->missing_logged = false;
	if(st.st_size <= 0){
		if(!ctx->has_last_stat || st.st_size != ctx->last_size || st.st_mtim.tv_sec != ctx->last_mtime_sec || st.st_mtim.tv_nsec != ctx->last_mtime_nsec){
			mosquitto_log_printf(MOSQ_LOG_WARNING,
					"hvac_crl: ignoring empty crlfile %s; preserving previous CRL",
					ctx->crlfile);
			record_stat(ctx, &st);
		}
		return false;
	}

	if(!force && ctx->has_last_stat
			&& st.st_mtim.tv_sec == ctx->last_mtime_sec
			&& st.st_mtim.tv_nsec == ctx->last_mtime_nsec
			&& st.st_ino == ctx->last_ino
			&& st.st_size == ctx->last_size){
		return false;
	}

	BIO *bio = BIO_new_file(ctx->crlfile, "rb");
	if(!bio){
		mosquitto_log_printf(MOSQ_LOG_ERR,
				"hvac_crl: failed to open crlfile %s (%s); preserving previous CRL",
				ctx->crlfile, strerror(errno));
		return false;
	}

	ERR_clear_error();
	X509_CRL *candidate = PEM_read_bio_X509_CRL(bio, NULL, NULL, NULL);
	BIO_free(bio);
	if(!candidate){
		mosquitto_log_printf(MOSQ_LOG_ERR,
				"hvac_crl: failed to parse PEM CRL from %s; preserving previous CRL",
				ctx->crlfile);
		record_stat(ctx, &st);
		return false;
	}

	if(!verify_crl_against_cas(ctx, candidate)){
		X509_CRL_free(candidate);
		mosquitto_log_printf(MOSQ_LOG_ERR,
				"hvac_crl: CRL signature or issuer verification failed for %s; preserving previous CRL",
				ctx->crlfile);
		record_stat(ctx, &st);
		return false;
	}

	X509_CRL_free(ctx->active_crl);
	ctx->active_crl = candidate;
	ctx->active_crl_issuer = (X509_NAME *)X509_CRL_get_issuer(candidate);
	record_stat(ctx, &st);

	const STACK_OF(X509_REVOKED) *revoked = X509_CRL_get_REVOKED(candidate);
	int revoked_count = revoked ? sk_X509_REVOKED_num(revoked) : 0;
	mosquitto_log_printf(MOSQ_LOG_NOTICE,
			"hvac_crl: loaded verified CRL from %s (%d revoked serial(s))",
			ctx->crlfile, revoked_count);
	return true;
}

static bool cert_parts_revoked(const struct crl_plugin_ctx *ctx, const X509_NAME *issuer, const ASN1_INTEGER *serial)
{
	if(!ctx->active_crl || !ctx->active_crl_issuer || !issuer || !serial){
		return false;
	}
	if(X509_NAME_cmp(issuer, ctx->active_crl_issuer) != 0){
		return false;
	}
	X509_REVOKED *entry = NULL;
	return X509_CRL_get0_by_serial(ctx->active_crl, &entry, serial) == 1;
}

static bool is_x509_revoked(const struct crl_plugin_ctx *ctx, const X509 *cert)
{
	if(!ctx->active_crl || !cert){
		return false;
	}
	return cert_parts_revoked(ctx, X509_get_issuer_name(cert), X509_get0_serialNumber(cert));
}

static bool format_cert_fingerprint(const X509 *cert, char out[65])
{
	if(!cert){
		return false;
	}
	unsigned char digest[EVP_MAX_MD_SIZE];
	unsigned int digest_len = 0;
	if(X509_digest(cert, EVP_sha256(), digest, &digest_len) != 1 || digest_len != 32){
		return false;
	}
	static const char hex[] = "0123456789abcdef";
	for(unsigned int i = 0; i < 32; i++){
		out[i * 2] = hex[(digest[i] >> 4) & 0x0f];
		out[i * 2 + 1] = hex[digest[i] & 0x0f];
	}
	out[64] = '\0';
	return true;
}

static void upsert_client(struct crl_plugin_ctx *ctx, const struct mosquitto *client, X509 *borrowed_cert)
{
	if(!client){
		return;
	}
	const char *cid = mosquitto_client_id(client);
	if(!cid || cid[0] == '\0'){
		return;
	}
	const char *uname = mosquitto_client_username(client);

	X509 *local_cert = borrowed_cert;
	bool owns_cert = false;
	if(!local_cert){
		local_cert = (X509 *)mosquitto_client_certificate(client);
		owns_cert = (local_cert != NULL);
	}

	struct client_entry *existing = NULL;
	for(struct client_entry *cur = ctx->clients; cur != NULL; cur = cur->next){
		if(cur->client_ptr == client || strcmp(cur->clientid, cid) == 0){
			existing = cur;
			break;
		}
	}

	if(!existing){
		existing = calloc(1, sizeof(*existing));
		if(!existing){
			if(owns_cert){
				X509_free(local_cert);
			}
			return;
		}
		existing->clientid = dup_or_null(cid);
		if(!existing->clientid){
			free(existing);
			if(owns_cert){
				X509_free(local_cert);
			}
			return;
		}
		existing->next = ctx->clients;
		ctx->clients = existing;
	}else if(strcmp(existing->clientid, cid) != 0){
		char *new_cid = dup_or_null(cid);
		if(new_cid){
			free(existing->clientid);
			existing->clientid = new_cid;
		}
	}

	existing->client_ptr = client;

	if(uname){
		if(!existing->username || strcmp(existing->username, uname) != 0){
			char *new_uname = dup_or_null(uname);
			if(new_uname){
				free(existing->username);
				existing->username = new_uname;
			}
		}
	}else{
		free(existing->username);
		existing->username = NULL;
	}

	if(local_cert){
		X509_NAME *issuer = X509_NAME_dup(X509_get_issuer_name(local_cert));
		ASN1_INTEGER *serial = ASN1_INTEGER_dup(X509_get0_serialNumber(local_cert));
		char fp[65];
		bool has_fp = format_cert_fingerprint(local_cert, fp);

		if(issuer){
			X509_NAME_free(existing->issuer);
			existing->issuer = issuer;
		}
		if(serial){
			ASN1_INTEGER_free(existing->serial);
			existing->serial = serial;
		}
		if(has_fp){
			memcpy(existing->fingerprint_sha256, fp, 65);
		}
		if(owns_cert){
			X509_free(local_cert);
		}
	}
}

static void remove_client(struct crl_plugin_ctx *ctx, const struct mosquitto *client)
{
	if(!client){
		return;
	}
	const char *cid = mosquitto_client_id(client);
	struct client_entry **link = &ctx->clients;
	while(*link){
		struct client_entry *cur = *link;
		bool match = (cur->client_ptr == client);
		if(!match && cid && cid[0] != '\0' && strcmp(cur->clientid, cid) == 0 && cur->client_ptr == NULL){
			match = true;
		}
		if(match){
			*link = cur->next;
			free_client_entry(cur);
			return;
		}
		link = &cur->next;
	}
}

typedef bool (*kick_predicate_fn)(const struct client_entry *entry, void *arg);

static int kick_matching_clients(struct crl_plugin_ctx *ctx, kick_predicate_fn pred, void *arg, bool with_will)
{
	size_t cap = 0;
	size_t count = 0;
	char **targets = NULL;

	for(struct client_entry *cur = ctx->clients; cur != NULL; cur = cur->next){
		if(!cur->clientid || !pred(cur, arg)){
			continue;
		}
		if(count == cap){
			size_t next_cap = cap == 0 ? 8 : cap * 2;
			char **grown = realloc(targets, next_cap * sizeof(*targets));
			if(!grown){
				break;
			}
			targets = grown;
			cap = next_cap;
		}
		char *copy = strdup(cur->clientid);
		if(!copy){
			break;
		}
		targets[count++] = copy;
	}

	int kicked = 0;
	for(size_t i = 0; i < count; i++){
		if(with_will){
			ctx->will_grace_clientid = targets[i];
		}
		int rc = mosquitto_kick_client_by_clientid(targets[i], with_will);
		ctx->will_grace_clientid = NULL;
		if(rc == MOSQ_ERR_SUCCESS){
			kicked++;
		}
		free(targets[i]);
	}
	free(targets);
	return kicked;
}

static bool pred_crl_revoked(const struct client_entry *entry, void *arg)
{
	const struct crl_plugin_ctx *ctx = (const struct crl_plugin_ctx *)arg;
	return cert_parts_revoked(ctx, entry->issuer, entry->serial);
}

struct control_kick_filter {
	const struct crl_plugin_ctx *ctx;
	const char *gateway_id;
	const char *fingerprint_sha256;
	bool revoke_all;
};

static bool pred_control_target(const struct client_entry *entry, void *arg)
{
	const struct control_kick_filter *f = (const struct control_kick_filter *)arg;
	if(f->revoke_all){
		if(f->gateway_id && entry->username && strcmp(entry->username, f->gateway_id) == 0){
			return true;
		}
	}else if(f->fingerprint_sha256 && entry->fingerprint_sha256[0] != '\0'){
		if(strcasecmp(entry->fingerprint_sha256, f->fingerprint_sha256) == 0){
			if(!f->gateway_id || (entry->username && strcmp(entry->username, f->gateway_id) == 0)){
				return true;
			}
		}
	}
	return cert_parts_revoked(f->ctx, entry->issuer, entry->serial);
}

static int sweep_crl_revoked(struct crl_plugin_ctx *ctx, bool with_will)
{
	if(!ctx->active_crl){
		return 0;
	}
	return kick_matching_clients(ctx, pred_crl_revoked, ctx, with_will);
}

static int on_basic_auth(int event, void *event_data, void *userdata)
{
	(void)event;
	struct crl_plugin_ctx *ctx = (struct crl_plugin_ctx *)userdata;
	struct mosquitto_evt_basic_auth *ed = (struct mosquitto_evt_basic_auth *)event_data;
	X509 *cert = (X509 *)mosquitto_client_certificate(ed->client);
	if(is_x509_revoked(ctx, cert)){
		mosquitto_log_printf(MOSQ_LOG_WARNING,
				"hvac_crl: rejected revoked certificate during basic auth for client %s",
				mosquitto_client_id(ed->client) ? mosquitto_client_id(ed->client) : "<unknown>");
		X509_free(cert);
		return MOSQ_ERR_AUTH;
	}
	upsert_client(ctx, ed->client, cert);
	X509_free(cert);
	return MOSQ_ERR_PLUGIN_DEFER;
}

static int on_connect(int event, void *event_data, void *userdata)
{
	(void)event;
	struct crl_plugin_ctx *ctx = (struct crl_plugin_ctx *)userdata;
	struct mosquitto_evt_connect *ed = (struct mosquitto_evt_connect *)event_data;
	const char *cid = mosquitto_client_id(ed->client);
	X509 *cert = (X509 *)mosquitto_client_certificate(ed->client);
	if(is_x509_revoked(ctx, cert)){
		mosquitto_log_printf(MOSQ_LOG_WARNING,
				"hvac_crl: disconnecting revoked certificate on connect for client %s (username=%s)",
				cid ? cid : "<unknown>",
				mosquitto_client_username(ed->client) ? mosquitto_client_username(ed->client) : "<none>");
		X509_free(cert);
		if(cid && cid[0] != '\0'){
			mosquitto_kick_client_by_clientid(cid, false);
		}
		return MOSQ_ERR_SUCCESS;
	}
	upsert_client(ctx, ed->client, cert);
	X509_free(cert);
	return MOSQ_ERR_SUCCESS;
}

static int on_acl_check(int event, void *event_data, void *userdata)
{
	(void)event;
	struct crl_plugin_ctx *ctx = (struct crl_plugin_ctx *)userdata;
	struct mosquitto_evt_acl_check *ed = (struct mosquitto_evt_acl_check *)event_data;
	const char *cid = mosquitto_client_id(ed->client);
	if(ctx->will_grace_clientid && cid && strcmp(ctx->will_grace_clientid, cid) == 0){
		/* Allow the LWT published by an intentional with_will kick to flow through acl_file. */
		return MOSQ_ERR_PLUGIN_DEFER;
	}
	X509 *cert = (X509 *)mosquitto_client_certificate(ed->client);
	if(is_x509_revoked(ctx, cert)){
		mosquitto_log_printf(MOSQ_LOG_WARNING,
				"hvac_crl: denied ACL and kicking revoked certificate for client %s",
				cid ? cid : "<unknown>");
		X509_free(cert);
		if(cid && cid[0] != '\0'){
			mosquitto_kick_client_by_clientid(cid, false);
		}
		return MOSQ_ERR_ACL_DENIED;
	}
	upsert_client(ctx, ed->client, cert);
	X509_free(cert);
	return MOSQ_ERR_PLUGIN_DEFER;
}

static int on_disconnect(int event, void *event_data, void *userdata)
{
	(void)event;
	struct crl_plugin_ctx *ctx = (struct crl_plugin_ctx *)userdata;
	struct mosquitto_evt_disconnect *ed = (struct mosquitto_evt_disconnect *)event_data;
	remove_client(ctx, ed->client);
	return MOSQ_ERR_SUCCESS;
}

static int on_client_offline(int event, void *event_data, void *userdata)
{
	(void)event;
	struct crl_plugin_ctx *ctx = (struct crl_plugin_ctx *)userdata;
	struct mosquitto_evt_client_offline *ed = (struct mosquitto_evt_client_offline *)event_data;
	remove_client(ctx, ed->client);
	return MOSQ_ERR_SUCCESS;
}

static bool is_valid_sha256_hex(const char *s)
{
	if(!s || strlen(s) != 64){
		return false;
	}
	for(size_t i = 0; i < 64; i++){
		char c = s[i];
		bool ok = (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F');
		if(!ok){
			return false;
		}
	}
	return true;
}

static int on_control(int event, void *event_data, void *userdata)
{
	(void)event;
	struct crl_plugin_ctx *ctx = (struct crl_plugin_ctx *)userdata;
	struct mosquitto_evt_control *ed = (struct mosquitto_evt_control *)event_data;

	const char *username = mosquitto_client_username(ed->client);
	if(!username || strcmp(username, ctx->control_user) != 0){
		mosquitto_log_printf(MOSQ_LOG_WARNING,
				"hvac_crl: rejected unauthorized $CONTROL publish by user %s",
				username ? username : "<anonymous>");
		return MOSQ_ERR_ACL_DENIED;
	}

	if(!ed->payload || ed->payloadlen == 0 || ed->payloadlen > MAX_CONTROL_PAYLOAD_BYTES){
		return MOSQ_ERR_INVAL;
	}

	char *json_buf = malloc((size_t)ed->payloadlen + 1);
	if(!json_buf){
		return MOSQ_ERR_NOMEM;
	}
	memcpy(json_buf, ed->payload, ed->payloadlen);
	json_buf[ed->payloadlen] = '\0';

	cJSON *root = cJSON_Parse(json_buf);
	free(json_buf);
	if(!root || !cJSON_IsObject(root)){
		cJSON_Delete(root);
		return MOSQ_ERR_INVAL;
	}

	const cJSON *action_item = cJSON_GetObjectItemCaseSensitive(root, "action");
	const cJSON *gw_item = cJSON_GetObjectItemCaseSensitive(root, "gatewayId");
	const cJSON *fp_item = cJSON_GetObjectItemCaseSensitive(root, "fingerprintSha256");
	const cJSON *revoke_all_item = cJSON_GetObjectItemCaseSensitive(root, "revokeAll");
	const cJSON *with_will_item = cJSON_GetObjectItemCaseSensitive(root, "withWill");

	const char *action = cJSON_IsString(action_item) ? action_item->valuestring : NULL;
	const char *gateway_id = (cJSON_IsString(gw_item) && gw_item->valuestring[0] != '\0') ? gw_item->valuestring : NULL;
	const char *fingerprint = cJSON_IsString(fp_item) ? fp_item->valuestring : NULL;
	bool revoke_all = cJSON_IsBool(revoke_all_item) && cJSON_IsTrue(revoke_all_item);
	bool with_will = cJSON_IsBool(with_will_item) && cJSON_IsTrue(with_will_item);

	if(!action || (strcmp(action, "reload") != 0 && strcmp(action, "reload_and_kick") != 0)){
		cJSON_Delete(root);
		return MOSQ_ERR_INVAL;
	}
	if(fingerprint && fingerprint[0] != '\0' && !is_valid_sha256_hex(fingerprint)){
		cJSON_Delete(root);
		return MOSQ_ERR_INVAL;
	}

	reload_crl_if_changed(ctx, true);

	int kicked = 0;
	if(strcmp(action, "reload_and_kick") == 0){
		struct control_kick_filter filter = {
			.ctx = ctx,
			.gateway_id = gateway_id,
			.fingerprint_sha256 = (fingerprint && fingerprint[0] != '\0') ? fingerprint : NULL,
			.revoke_all = revoke_all,
		};
		kicked = kick_matching_clients(ctx, pred_control_target, &filter, with_will);
	}else{
		kicked = sweep_crl_revoked(ctx, false);
	}

	mosquitto_log_printf(MOSQ_LOG_NOTICE,
			"hvac_crl: control action=%s gateway=%s revokeAll=%d withWill=%d kicked=%d",
			action, gateway_id ? gateway_id : "-", revoke_all ? 1 : 0, with_will ? 1 : 0, kicked);

	cJSON_Delete(root);
	return MOSQ_ERR_SUCCESS;
}

static int on_tick(int event, void *event_data, void *userdata)
{
	(void)event;
	struct crl_plugin_ctx *ctx = (struct crl_plugin_ctx *)userdata;
	struct mosquitto_evt_tick *ed = (struct mosquitto_evt_tick *)event_data;
	ed->next_s = ctx->poll_seconds;

	if(ed->now_s - ctx->last_poll_s < ctx->poll_seconds){
		return MOSQ_ERR_SUCCESS;
	}
	ctx->last_poll_s = ed->now_s;
	if(reload_crl_if_changed(ctx, false)){
		int kicked = sweep_crl_revoked(ctx, false);
		if(kicked > 0){
			mosquitto_log_printf(MOSQ_LOG_NOTICE,
					"hvac_crl: kicked %d active client(s) after CRL file change",
					kicked);
		}
	}
	return MOSQ_ERR_SUCCESS;
}

static int on_reload(int event, void *event_data, void *userdata)
{
	(void)event;
	(void)event_data;
	struct crl_plugin_ctx *ctx = (struct crl_plugin_ctx *)userdata;
	(void)load_ca_bundle(ctx);
	(void)reload_crl_if_changed(ctx, true);
	int kicked = sweep_crl_revoked(ctx, false);
	if(kicked > 0){
		mosquitto_log_printf(MOSQ_LOG_NOTICE,
				"hvac_crl: kicked %d active client(s) on broker reload",
				kicked);
	}
	return MOSQ_ERR_SUCCESS;
}

static int register_required(struct crl_plugin_ctx *ctx, int event, MOSQ_FUNC_generic_callback cb, const void *match)
{
	int rc = mosquitto_callback_register(ctx->plugin_id, event, cb, match, ctx);
	if(rc != MOSQ_ERR_SUCCESS){
		mosquitto_log_printf(MOSQ_LOG_ERR,
				"hvac_crl: failed to register event %d (rc=%d)",
				event, rc);
	}
	return rc;
}

int mosquitto_plugin_init(mosquitto_plugin_id_t *identifier, void **userdata, struct mosquitto_opt *opts, int opt_count)
{
	struct crl_plugin_ctx *ctx = calloc(1, sizeof(*ctx));
	if(!ctx){
		return MOSQ_ERR_NOMEM;
	}
	ctx->plugin_id = identifier;
	ctx->poll_seconds = 1;

	for(int i = 0; i < opt_count; i++){
		if(!opts[i].key || !opts[i].value){
			continue;
		}
		if(strcmp(opts[i].key, "cafile") == 0 && opts[i].value[0] != '\0'){
			free(ctx->cafile);
			ctx->cafile = dup_or_null(opts[i].value);
		}else if(strcmp(opts[i].key, "crlfile") == 0 && opts[i].value[0] != '\0'){
			free(ctx->crlfile);
			ctx->crlfile = dup_or_null(opts[i].value);
		}else if(strcmp(opts[i].key, "control_user") == 0 && opts[i].value[0] != '\0'){
			free(ctx->control_user);
			ctx->control_user = dup_or_null(opts[i].value);
		}else if(strcmp(opts[i].key, "poll_seconds") == 0){
			long v = strtol(opts[i].value, NULL, 10);
			if(v >= 1 && v <= 3600){
				ctx->poll_seconds = (time_t)v;
			}
		}
	}

	if(!ctx->cafile){
		ctx->cafile = dup_or_null(DEFAULT_CAFILE);
	}
	if(!ctx->crlfile){
		ctx->crlfile = dup_or_null(DEFAULT_CRLFILE);
	}
	if(!ctx->control_user){
		ctx->control_user = dup_or_null(DEFAULT_CONTROL_USER);
	}
	if(!ctx->cafile || !ctx->crlfile || !ctx->control_user){
		mosquitto_plugin_cleanup(ctx, opts, opt_count);
		return MOSQ_ERR_NOMEM;
	}

#if LIBMOSQUITTO_VERSION_NUMBER >= 2001000
	(void)mosquitto_plugin_set_info(identifier, "hvac-crl", "1.0.0");
#endif

	if(!load_ca_bundle(ctx)){
		mosquitto_plugin_cleanup(ctx, opts, opt_count);
		return MOSQ_ERR_UNKNOWN;
	}
	(void)reload_crl_if_changed(ctx, true);

	if(register_required(ctx, MOSQ_EVT_BASIC_AUTH, on_basic_auth, NULL) != MOSQ_ERR_SUCCESS
			|| register_required(ctx, MOSQ_EVT_ACL_CHECK, on_acl_check, NULL) != MOSQ_ERR_SUCCESS
			|| register_required(ctx, MOSQ_EVT_DISCONNECT, on_disconnect, NULL) != MOSQ_ERR_SUCCESS
			|| register_required(ctx, MOSQ_EVT_CONTROL, on_control, CONTROL_TOPIC) != MOSQ_ERR_SUCCESS){
		mosquitto_plugin_cleanup(ctx, opts, opt_count);
		return MOSQ_ERR_UNKNOWN;
	}

	if(mosquitto_callback_register(ctx->plugin_id, MOSQ_EVT_CONNECT, on_connect, NULL, ctx) == MOSQ_ERR_SUCCESS){
		ctx->registered_connect = true;
	}else{
		mosquitto_log_printf(MOSQ_LOG_WARNING,
				"hvac_crl: MOSQ_EVT_CONNECT unavailable on this broker; falling back to ACL hook");
	}
	if(mosquitto_callback_register(ctx->plugin_id, MOSQ_EVT_CLIENT_OFFLINE, on_client_offline, NULL, ctx) == MOSQ_ERR_SUCCESS){
		ctx->registered_offline = true;
	}
	if(mosquitto_callback_register(ctx->plugin_id, MOSQ_EVT_RELOAD, on_reload, NULL, ctx) == MOSQ_ERR_SUCCESS){
		ctx->registered_reload = true;
	}
	if(mosquitto_callback_register(ctx->plugin_id, MOSQ_EVT_TICK, on_tick, NULL, ctx) == MOSQ_ERR_SUCCESS){
		ctx->registered_tick = true;
	}

	*userdata = ctx;
	mosquitto_log_printf(MOSQ_LOG_NOTICE,
			"hvac_crl: initialized (crlfile=%s, control_user=%s, poll_seconds=%ld)",
			ctx->crlfile, ctx->control_user, (long)ctx->poll_seconds);
	return MOSQ_ERR_SUCCESS;
}

int mosquitto_plugin_cleanup(void *userdata, struct mosquitto_opt *opts, int opt_count)
{
	(void)opts;
	(void)opt_count;
	struct crl_plugin_ctx *ctx = (struct crl_plugin_ctx *)userdata;
	if(!ctx){
		return MOSQ_ERR_SUCCESS;
	}

	if(ctx->plugin_id){
		(void)mosquitto_callback_unregister(ctx->plugin_id, MOSQ_EVT_BASIC_AUTH, on_basic_auth, NULL);
		(void)mosquitto_callback_unregister(ctx->plugin_id, MOSQ_EVT_ACL_CHECK, on_acl_check, NULL);
		(void)mosquitto_callback_unregister(ctx->plugin_id, MOSQ_EVT_DISCONNECT, on_disconnect, NULL);
		(void)mosquitto_callback_unregister(ctx->plugin_id, MOSQ_EVT_CONTROL, on_control, CONTROL_TOPIC);
		if(ctx->registered_connect){
			(void)mosquitto_callback_unregister(ctx->plugin_id, MOSQ_EVT_CONNECT, on_connect, NULL);
		}
		if(ctx->registered_offline){
			(void)mosquitto_callback_unregister(ctx->plugin_id, MOSQ_EVT_CLIENT_OFFLINE, on_client_offline, NULL);
		}
		if(ctx->registered_reload){
			(void)mosquitto_callback_unregister(ctx->plugin_id, MOSQ_EVT_RELOAD, on_reload, NULL);
		}
		if(ctx->registered_tick){
			(void)mosquitto_callback_unregister(ctx->plugin_id, MOSQ_EVT_TICK, on_tick, NULL);
		}
	}

	free_all_clients(ctx);
	free_ca_list(ctx);
	X509_CRL_free(ctx->active_crl);
	free(ctx->cafile);
	free(ctx->crlfile);
	free(ctx->control_user);
	free(ctx);
	return MOSQ_ERR_SUCCESS;
}
