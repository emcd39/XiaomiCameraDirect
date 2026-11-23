# Publishing Xiaomi Camera Direct for HACS

HACS installs the integration in `custom_components/xiaomi_direct_pilot` from
[moukas/XiaomiCameraDirect](https://github.com/moukas/XiaomiCameraDirect).
Users add the repository as a custom **Integration**. The internal domain is
also the installed folder name and identifies existing HA config entries.

The root `hacs.json` declares the minimum Home Assistant version. Set the
integration version in `custom_components/xiaomi_direct_pilot/manifest.json`
when publishing an update. Keep translations, frontend resources and brand
images within the integration folder.

Run the relevant checks in the [test guide](../tests/README.md). GitHub Actions
validates the repository with HACS and Hassfest. HACS installs from the default
branch while there are no GitHub Releases. To publish a versioned release,
create a GitHub Release with a tag matching the manifest version.

The standalone Go gateway and the manually installed
`legacy/custom_components/xiaomi_local_gateway` adapter share this repository.
The [README](../README.md#xiaomi-local-gateway) describes their installation.
