// attrs: print allocation attributes for each path read from stdin.
// path \t st_blocks*512 \t ALLOCSIZE \t PRIVATESIZE \t CLONEID \t CLONE_REFCNT
#include <stdio.h>
#include <string.h>
#include <stdint.h>
#include <unistd.h>
#include <sys/attr.h>
#include <sys/stat.h>

struct __attribute__((packed)) reply {
	uint32_t length;
	attribute_set_t ret;
	uint8_t rest[256];
};

int main(void) {
	char path[4096];
	while (fgets(path, sizeof path, stdin)) {
		path[strcspn(path, "\n")] = 0;
		struct stat st;
		if (lstat(path, &st) != 0) { printf("%s\tERR-lstat\n", path); continue; }
		struct attrlist al;
		memset(&al, 0, sizeof al);
		al.bitmapcount = ATTR_BIT_MAP_COUNT;
		al.commonattr = ATTR_CMN_RETURNED_ATTRS;
		al.fileattr = ATTR_FILE_ALLOCSIZE;
		al.forkattr = ATTR_CMNEXT_PRIVATESIZE | ATTR_CMNEXT_CLONEID | ATTR_CMNEXT_CLONE_REFCNT;
		struct reply r;
		memset(&r, 0, sizeof r);
		if (getattrlist(path, &al, &r, sizeof r, FSOPT_NOFOLLOW | FSOPT_ATTR_CMN_EXTENDED) != 0) {
			printf("%s\t%lld\tERR-getattrlist\n", path, (long long)st.st_blocks * 512);
			continue;
		}
		uint8_t *p = r.rest;
		long long alloc = -1, priv = -1; unsigned long long cid = 0; long long rc = -1; int hascid = 0;
		if (r.ret.fileattr & ATTR_FILE_ALLOCSIZE) { int64_t v; memcpy(&v, p, 8); p += 8; alloc = v; }
		if (r.ret.forkattr & ATTR_CMNEXT_PRIVATESIZE) { int64_t v; memcpy(&v, p, 8); p += 8; priv = v; }
		if (r.ret.forkattr & ATTR_CMNEXT_CLONEID) { uint64_t v; memcpy(&v, p, 8); p += 8; cid = v; hascid = 1; }
		if (r.ret.forkattr & ATTR_CMNEXT_CLONE_REFCNT) { uint32_t v; memcpy(&v, p, 4); p += 4; rc = v; }
		printf("%s\t%lld\t%lld\t%lld\t%lld\t%lld\n", path, (long long)st.st_blocks * 512, alloc, priv, hascid ? (long long)cid : -1, rc);
	}
	return 0;
}
