// walk: compare per-entry fstatat vs getattrlistbulk over one tree (read-only).
// usage: walk (stat|bulk) dir
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <stdint.h>
#include <fcntl.h>
#include <dirent.h>
#include <unistd.h>
#include <errno.h>
#include <sys/attr.h>
#include <sys/stat.h>
#include <sys/resource.h>
#include <sys/time.h>

#define VDIR 2 /* fsobj_type_t: VREG=1 VDIR=2 VLNK=5 (sys/vnode.h is not public) */
static long long files, dirs, bytes_blocks, bytes_alloc, bytes_priv, calls;

static void walk_stat(int dfd, const char *path) {
	int fd = openat(dfd, path, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
	if (fd < 0) return;
	DIR *d = fdopendir(fd);
	if (!d) { close(fd); return; }
	struct dirent *e;
	while ((e = readdir(d))) {
		if (!strcmp(e->d_name, ".") || !strcmp(e->d_name, "..")) continue;
		struct stat st;
		calls++;
		if (fstatat(fd, e->d_name, &st, AT_SYMLINK_NOFOLLOW) != 0) continue;
		bytes_blocks += (long long)st.st_blocks * 512;
		if (S_ISDIR(st.st_mode)) { dirs++; walk_stat(fd, e->d_name); }
		else files++;
	}
	closedir(d);
}

struct __attribute__((packed)) ent_hdr { uint32_t length; attribute_set_t ret; };

static void walk_bulk(int dfd, const char *path) {
	int fd = openat(dfd, path, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
	if (fd < 0) return;
	struct attrlist al;
	memset(&al, 0, sizeof al);
	al.bitmapcount = ATTR_BIT_MAP_COUNT;
	al.commonattr = ATTR_CMN_RETURNED_ATTRS | ATTR_CMN_NAME | ATTR_CMN_OBJTYPE;
	al.fileattr = ATTR_FILE_ALLOCSIZE;
	al.forkattr = ATTR_CMNEXT_PRIVATESIZE;
	static __thread char *buf; // one 256 KiB buffer per recursion would be too much; allocate per call
	size_t bufsz = 256 * 1024;
	char *b = malloc(bufsz);
	// collect subdirs to recurse after reading (keeps fd count low)
	char **subs = NULL; size_t nsubs = 0, capsubs = 0;
	for (;;) {
		calls++;
		int n = getattrlistbulk(fd, &al, b, bufsz, FSOPT_NOFOLLOW | FSOPT_ATTR_CMN_EXTENDED);
		if (n <= 0) break;
		char *p = b;
		for (int i = 0; i < n; i++) {
			uint32_t len; memcpy(&len, p, 4);
			char *q = p + 4;
			attribute_set_t ret; memcpy(&ret, q, sizeof ret); q += sizeof ret;
			char *name = NULL; uint32_t objtype = 0; long long alloc = 0, priv = 0;
			if (ret.commonattr & ATTR_CMN_NAME) {
				attrreference_t ref; memcpy(&ref, q, sizeof ref);
				name = q + ref.attr_dataoffset; q += sizeof ref;
			}
			if (ret.commonattr & ATTR_CMN_OBJTYPE) { memcpy(&objtype, q, 4); q += 4; }
			if (ret.fileattr & ATTR_FILE_ALLOCSIZE) { int64_t v; memcpy(&v, q, 8); q += 8; alloc = v; }
			if (ret.forkattr & ATTR_CMNEXT_PRIVATESIZE) { int64_t v; memcpy(&v, q, 8); q += 8; priv = v; }
			if (objtype == VDIR) {
				dirs++;
				if (nsubs == capsubs) { capsubs = capsubs ? capsubs * 2 : 16; subs = realloc(subs, capsubs * sizeof *subs); }
				subs[nsubs++] = strdup(name);
			} else {
				files++; bytes_alloc += alloc; bytes_priv += priv;
			}
			p += len;
		}
	}
	free(b);
	for (size_t i = 0; i < nsubs; i++) { walk_bulk(fd, subs[i]); free(subs[i]); }
	free(subs);
	close(fd);
	(void)buf;
}

int main(int argc, char **argv) {
	if (argc < 3) return 2;
	struct rusage a, b2; struct timeval t0, t1;
	getrusage(RUSAGE_SELF, &a); gettimeofday(&t0, NULL);
	if (!strcmp(argv[1], "stat")) walk_stat(AT_FDCWD, argv[2]); else walk_bulk(AT_FDCWD, argv[2]);
	gettimeofday(&t1, NULL); getrusage(RUSAGE_SELF, &b2);
	double wall = (t1.tv_sec - t0.tv_sec) + (t1.tv_usec - t0.tv_usec) / 1e6;
	double cpu = (b2.ru_utime.tv_sec - a.ru_utime.tv_sec) + (b2.ru_utime.tv_usec - a.ru_utime.tv_usec) / 1e6
		   + (b2.ru_stime.tv_sec - a.ru_stime.tv_sec) + (b2.ru_stime.tv_usec - a.ru_stime.tv_usec) / 1e6;
	printf("%s files=%lld dirs=%lld syscalls=%lld wall=%.3fs cpu=%.3fs blocks=%lld alloc=%lld private=%lld\n",
		argv[1], files, dirs, calls, wall, cpu, bytes_blocks, bytes_alloc, bytes_priv);
	return 0;
}
