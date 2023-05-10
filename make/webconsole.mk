WEBCONSOLE_SRC_DIR = $(PWD)/webconsole_source
DIST_DIR = $(WEBCONSOLE_SRC_DIR)/dist
TARGET_DIR = $(PWD)/pkg/consoleplugin/contentserver/static

.PHONY: generate-webconsole-scripts
# This target will fire up a container that will compile the source files found in the /webconsole_source directory.
# The new files will then be copied over the top of the existing static content in /pkg/consoleplugin/contentserver/static,
# overwriting what is there.  The newly compiled files should then be committed to the repo.
generate-webconsole-scripts:
	mkdir -p $(DIST_DIR)
	rm -f $(DIST_DIR)/*
	cd $(WEBCONSOLE_SRC_DIR);${IMAGE_BUILDER} build --no-cache --volume $(DIST_DIR):/opt/app-root/dist:U -f $(WEBCONSOLE_SRC_DIR)/Containerfile .
	rm -f $(TARGET_DIR)/*
	cp $(DIST_DIR)/* $(TARGET_DIR)/
